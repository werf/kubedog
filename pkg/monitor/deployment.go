package monitor

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	extensions "k8s.io/api/extensions/v1beta1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"
	//deployutil "k8s.io/kubernetes/pkg/controller/deployment/util"
)

// DeploymentRollout interface for rollout process callbacks
type DeploymentRollout interface {
	Started() error
	Succeeded() error
	Failed() error
	//LogChunk(*PodLogChunk) error
	//PodError(PodError) error
	// ...
}

// WatchDeploymentRollout is for monitor deployment rollout
func WatchDeploymentRollout(name string, namespace string, kube kubernetes.Interface, feed DeploymentRollout, opts WatchOptions) error {
	if debug() {
		fmt.Printf("> DeploymentRollout\n")
	}

	errorChan := make(chan error, 0)
	doneChan := make(chan bool, 0)

	parentContext := opts.ParentContext
	if parentContext == nil {
		parentContext = context.Background()
	}

	deploymentMon := NewDeploymentWatchMonitor(parentContext, name, namespace, kube, opts)

	go func() {
		fmt.Printf("  goroutine: start deploy/%s monitor watcher\n", name)
		err := deploymentMon.Watch()
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- true
		}
	}()

	if debug() {
		fmt.Printf("  deploy/%s: for-select deploymentWatchMonitor channels\n", name)
	}

	for {
		select {
		case <-deploymentMon.RolloutStarted:
			if debug() {
				fmt.Printf("    deploy/%s rollout started\n", name)
			}

			return feed.Started()

		case <-deploymentMon.RolloutSucceeded:
			if debug() {
				fmt.Printf("    deploy/%s rollout succeeded: desired: %d, current: %d/%d, up-to-date: %d, available: %d\n",
					name,
					deploymentMon.FinalDeploymentStatus.Replicas,
					deploymentMon.FinalDeploymentStatus.ReadyReplicas,
					deploymentMon.FinalDeploymentStatus.UnavailableReplicas,
					deploymentMon.FinalDeploymentStatus.UpdatedReplicas,
					deploymentMon.FinalDeploymentStatus.AvailableReplicas,
				)
			}

			return feed.Succeeded()

		case <-deploymentMon.RolloutFailed:
			if debug() {
				fmt.Printf("    deploy/%s rollout failed. Monitor state: `%s`", name, deploymentMon.State)
			}
			return feed.Failed()

		case chunk := <-deploymentMon.ContainerLogChunk:
			if debug() {
				fmt.Printf("    deploy/%s pod container `%s` log chunk\n%+v\n", name, chunk.ContainerName, chunk.LogLines)
			}

			// feed.LogChunk(...)

		case err := <-errorChan:
			return fmt.Errorf("deploy/%s error: %v", name, err)
		case <-doneChan:
			return nil
		}
	}
}

// DeploymentWatchMonitor ...
type DeploymentWatchMonitor struct {
	WatchMonitor

	Manifest      *extensions.Deployment
	NewReplicaSet *extensions.ReplicaSet

	State                 string
	Conditions            []string
	WaitForResource       bool
	FinalDeploymentStatus extensions.DeploymentStatus

	RolloutStarted   chan bool
	RolloutSucceeded chan bool
	RolloutFailed    chan bool

	AddedPod          chan *PodWatchMonitor
	ContainerLogChunk chan *ContainerLogChunk

	//PodError          chan PodError
	resourceAvailable chan *extensions.Deployment
	resourceModified  chan bool
	newReplicaSet     chan *extensions.ReplicaSet
	errors            chan error

	FailedReason chan error

	MonitoredPods []*PodWatchMonitor
}

// NewDeploymentWatchMonitor ...
func NewDeploymentWatchMonitor(ctx context.Context, name, namespace string, kube kubernetes.Interface, opts WatchOptions) *DeploymentWatchMonitor {
	if debug() {
		fmt.Printf("> NewDeploymentWatchMonitor\n")
	}
	return &DeploymentWatchMonitor{
		WatchMonitor: WatchMonitor{
			Kube: kube,
			//Timeout:      opts.Timeout,
			Namespace:    namespace,
			ResourceName: name,
			Context:      ctx,
		},

		WaitForResource: opts.WaitForResource,

		RolloutStarted:    make(chan bool, 0),
		RolloutSucceeded:  make(chan bool, 0),
		RolloutFailed:     make(chan bool, 0),
		ContainerLogChunk: make(chan *ContainerLogChunk, 1000),

		AddedPod:      make(chan *PodWatchMonitor, 10),
		MonitoredPods: make([]*PodWatchMonitor, 0),

		//PodError: make(chan PodError, 0),
		resourceAvailable: make(chan *extensions.Deployment, 0),
		resourceModified:  make(chan bool, 0),
		newReplicaSet:     make(chan *extensions.ReplicaSet, 0),
		errors:            make(chan error, 0),
	}
}

// Watch for deployment rollout process
// watch only for one deployment resource with name d.ResourceName within the namespace with name d.Namespace
// Watcher can wait for namespace creation and then for deployment creation
// watcher receives added event if deployment is started
// watch is infinite by default
// there is option StopOnAvailable — if true, watcher stops after deployment has available status
// you can define custom stop triggers using custom DeploymentFeed.
func (d *DeploymentWatchMonitor) Watch() (err error) {
	if debug() {
		fmt.Printf("> DeploymentWatchMonitor.Watch()\n")
	}
	//client := d.Kube

	go d.getOrWaitForDeployment()
WAIT_FOR_RESOURCE:
	for {
		select {
		// getOrWait returns existed deployment or a new deployment over d.ResourceAvailable channel
		case deployment := <-d.resourceAvailable:
			d.Manifest = deployment
			break WAIT_FOR_RESOURCE
		// error occured if no namespace or deployment available
		// or if event watcher has errors
		case err = <-d.errors:
			return fmt.Errorf("retrieve deploy/%s manifest error: %v", d.ResourceName, err)
		}
	}

	go d.waitForNewReplicaSet()
WAIT_FOR_NEW_REPLICA_SET:
	for {
		select {
		case replicaSet := <-d.newReplicaSet:
			d.NewReplicaSet = replicaSet
			fmt.Printf("")
			break WAIT_FOR_NEW_REPLICA_SET
		case err = <-d.errors:
			return fmt.Errorf("retrieve deploy/%s new replica set error: %v", d.ResourceName, err)
		}
	}
	// select{for{ d.NewReplicaSet }}

	// go d.runReplicaSetWatcher()
	// listen on channels
	for {
		select {
		// d.RolloutSucceeded
		// d.RolloutFailed
		case <-d.Context.Done():
			return ErrWatchTimeout
		case err := <-d.errors:
			return err
		}
	}

	return err
}

func (d *DeploymentWatchMonitor) getOrWaitForDeployment() {
	client := d.Kube

	// send deployment if namespace and deployment already exists
	// or send an error
	if !d.WaitForResource {
		_, err := client.CoreV1().Namespaces().Get(d.Namespace, metav1.GetOptions{})
		if err != nil {
			d.errors <- fmt.Errorf("Cannot get namespace `%s`: %v", d.Namespace, err)
			return
		}
		deployment, err := client.Extensions().
			Deployments(d.Namespace).
			Get(d.ResourceName, metav1.GetOptions{})
		if err != nil {
			fmt.Printf("get deployment error: %T [%v]", err, err)
			d.errors <- fmt.Errorf("deployment `%s` in namespace `%s` get error: %v", d.ResourceName, d.Namespace, err)
			return
		}
		d.resourceAvailable <- deployment
		return
	}

	// watch for deployment events
	tweakListOptions := func(options metav1.ListOptions) metav1.ListOptions {
		options.FieldSelector = fields.OneTermEqualSelector("metadata.name", d.ResourceName).String()
		return options
	}
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return client.Extensions().Deployments(d.Namespace).List(tweakListOptions(options))
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return client.Extensions().Deployments(d.Namespace).Watch(tweakListOptions(options))
		},
	}

	go func() {
		_, err := watchtools.UntilWithSync(d.Context, lw, &extensions.Deployment{}, nil, func(e watch.Event) (bool, error) {
			if debug() {
				fmt.Printf("    Deployment `%s` watcher Event: %#v\n", d.ResourceName, e.Type)
			}

			object, ok := e.Object.(*extensions.Deployment)
			if !ok {
				return true, fmt.Errorf("expect %s type *appvs1.Deployment, got %T", d.ResourceName, e.Object)
			}

			switch d.State {
			case "":
				if e.Type == watch.Added {
					if debug() {
						fmt.Printf("Deployment `%s` is ADDED\n", d.ResourceName)
					}
					d.State = "Started"
					d.resourceAvailable <- object

					//return true, nil

					// newRs, err := deployutil.GetNewReplicaSet(object, client)
					// if err != nil {
					// 	return false, err
					// }

					//fmt.Printf("Got new replica set ")

					// add pod monitor to MonitoredPods
					// go func() {
					// 	err := d.watchPods()
					// 	if err != nil {
					// 		d.Error <- err
					// 	}
					// }()
				}

				return d.handleStatusConditions(object)
			case "Started":
				if e.Type == watch.Deleted {
					d.State = "Deleted"
					d.RolloutFailed <- true
					return true, nil
				}
				if e.Type == watch.Modified {
					d.resourceModified <- true
				}
				return d.handleStatusConditions(object)
			default:
				return true, fmt.Errorf("unknown deploy/%s watcher state: %s", d.ResourceName, d.State)
			}
		})

		if err != nil {
			d.errors <- err
		}

		if debug() {
			fmt.Printf("      deploy/%s UntilWithSync DONE\n", d.ResourceName)
		}
	}()
}

func (d *DeploymentWatchMonitor) handleStatusConditions(object *extensions.Deployment) (watchDone bool, err error) {
	msgs := []string{}
	msgs = append(msgs, fmt.Sprintf("    deploy/%s\n      conditions:", d.ResourceName))
	for _, c := range object.Status.Conditions {
		msgs = append(msgs, fmt.Sprintf("        - %s - %s - %s: \"%s\"", c.Type, c.Status, c.Reason, c.Message))
	}

	deployment := object
	if d.Manifest != nil {
		deployment = d.Manifest
	}
	msgs = append(msgs, fmt.Sprintf("      status:\n        complete: %v\n        progressing: %v\n        timed-out: %v",
		DeploymentComplete(deployment, &object.Status),
		DeploymentProgressing(deployment, &object.Status),
		DeploymentTimedOut(deployment, &object.Status),
	))

	msgs = append(msgs, fmt.Sprintf("        generation: %d", object.Generation))
	msgs = append(msgs, fmt.Sprintf("        observed g: %d", object.Status.ObservedGeneration))

	if debug() {
		fmt.Printf("%s\n", strings.Join(msgs, "\n"))
	}

	//d.FinalDeploymentStatus = object.Status
	//d.Succeeded <- true

	// for _, c := range object.Status.Conditions {
	// 	if c.Type == extensions.DeploymentAvailable && c.Status == corev1.ConditionTrue {
	// 		d.State = "Available"
	// 		d.FinalDeploymentStatus = object.Status
	// 		d.Succeeded <- true
	// 	}
	// 	// if c.Type == appsv1.DeploymentProgressing && c.Status == corev1.ConditionTrue {
	// 	// 	d.FinalDeploymentStatus = object.Status
	// 	// }
	// 	// } else {
	// 	// 	d.State = "..."
	// 	// 	return true, nil
	// 	// }
	// }

	return false, nil
}

func (d *DeploymentWatchMonitor) waitForNewReplicaSet() {
	client := d.Kube

	_, allOlds, newRs, err := GetAllReplicaSets(d.Manifest, client)
	if err != nil {
		fmt.Printf("waitForNewReplicaSet error: %v\n", err)
	}

	fmt.Printf("      replicaSets:\n")
	for i, rs := range allOlds {
		fmt.Printf("        - old %d: rs/%s replicas: %d  ver: %s, gen: %d, obsgen: %d\n", i, rs.Name, *rs.Spec.Replicas, rs.ResourceVersion, rs.Generation, rs.Status.ObservedGeneration)
	}
	if newRs != nil {
		fmt.Printf("        - new: rs/%s replicas: %d  ver: %s, gen: %d, obsgen: %d\n", newRs.Name, *newRs.Spec.Replicas, newRs.ResourceVersion, newRs.Generation, newRs.Status.ObservedGeneration)
	}

	// Get list of ReplicaSets, check if new rs is there
	// if no new rs, then listen for deployment modified channel and get list again, check for new again until new rs appeared

	for {
		select {
		case <-d.resourceModified:
			_, allOlds, newRs, err := GetAllReplicaSets(d.Manifest, client)
			if err != nil {
				fmt.Printf("waitForNewReplicaSet error: %v\n", err)
			}

			fmt.Printf("      replicaSets:\n")
			for i, rs := range allOlds {
				fmt.Printf("        - old %d: rs/%s replicas: %d  ver: %s, gen: %d, obsgen: %d\n", i, rs.Name, *rs.Spec.Replicas, rs.ResourceVersion, rs.Generation, rs.Status.ObservedGeneration)
			}
			if newRs != nil {
				fmt.Printf("        - new: rs/%s replicas: %d  ver: %s, gen: %d, obsgen: %d\n", newRs.Name, *newRs.Spec.Replicas, newRs.ResourceVersion, newRs.Generation, newRs.Status.ObservedGeneration)
			}
		case <-d.Context.Done():
			return
		}
	}

	// //d.Manifest.Status.ObservedGeneration

	// // send deployment if namespace and deployment already exists
	// // or send an error
	// if !d.WaitForResource {
	// 	_, err := client.CoreV1().Namespaces().Get(d.Namespace, metav1.GetOptions{})
	// 	if err != nil {
	// 		d.errors <- fmt.Errorf("Cannot get namespace `%s`: %v", d.Namespace, err)
	// 		return
	// 	}
	// 	deployment, err := client.Extensions().
	// 		Deployments(d.Namespace).
	// 		Get(d.ResourceName, metav1.GetOptions{})
	// 	if err != nil {
	// 		fmt.Printf("get deployment error: %T [%v]", err, err)
	// 		d.errors <- fmt.Errorf("deployment `%s` in namespace `%s` get error: %v", d.ResourceName, d.Namespace, err)
	// 		return
	// 	}
	// 	d.resourceAvailable <- deployment
	// 	return
	// }

	// // watch for deployment events
	// tweakListOptions := func(options metav1.ListOptions) metav1.ListOptions {
	// 	options.FieldSelector = fields.OneTermEqualSelector("metadata.name", d.ResourceName).String()
	// 	return options
	// }
	// lw := &cache.ListWatch{
	// 	ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
	// 		return client.Extensions().Deployments(d.Namespace).List(tweakListOptions(options))
	// 	},
	// 	WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
	// 		return client.Extensions().Deployments(d.Namespace).Watch(tweakListOptions(options))
	// 	},
	// }

	// go func() {
	// 	_, err := watchtools.UntilWithSync(d.Context, lw, &extensions.Deployment{}, nil, func(e watch.Event) (bool, error) {
	// 		if debug() {
	// 			fmt.Printf("    Deployment `%s` watcher Event: %#v\n", d.ResourceName, e.Type)
	// 		}

	// 		object, ok := e.Object.(*extensions.Deployment)
	// 		if !ok {
	// 			return true, fmt.Errorf("expect %s type *appvs1.Deployment, got %T", d.ResourceName, e.Object)
	// 		}

	// 		switch d.State {
	// 		case "":
	// 			if e.Type == watch.Added {
	// 				if debug() {
	// 					fmt.Printf("Deployment `%s` is ADDED\n", d.ResourceName)
	// 				}
	// 				d.State = "Started"
	// 				d.resourceAvailable <- object

	// 				//return true, nil

	// 				// newRs, err := deployutil.GetNewReplicaSet(object, client)
	// 				// if err != nil {
	// 				// 	return false, err
	// 				// }

	// 				//fmt.Printf("Got new replica set ")

	// 				// add pod monitor to MonitoredPods
	// 				// go func() {
	// 				// 	err := d.watchPods()
	// 				// 	if err != nil {
	// 				// 		d.Error <- err
	// 				// 	}
	// 				// }()
	// 			}

	// 			return d.handleStatusConditions(object)
	// 		case "Started":
	// 			if e.Type == watch.Deleted {
	// 				d.State = "Deleted"
	// 				d.RolloutFailed <- true
	// 				return true, nil
	// 			}
	// 			return d.handleStatusConditions(object)
	// 		default:
	// 			return true, fmt.Errorf("unknown deploy/%s watcher state: %s", d.ResourceName, d.State)
	// 		}

	// 	})

	// 	if err != nil {
	// 		d.errors <- err
	// 	}

	// 	if debug() {
	// 		fmt.Printf("      deploy/%s UntilWithSync DONE\n", d.ResourceName)
	// 	}
	// }()
}

func (d *DeploymentWatchMonitor) watchPods() error {
	client := d.Kube

	deploymentManifest, err := client.AppsV1beta2().
		Deployments(d.Namespace).
		Get(d.ResourceName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("deployment `%s` in namespace `%s` get error: %v", d.ResourceName, d.Namespace, err)
	}
	selector, err := metav1.LabelSelectorAsSelector(deploymentManifest.Spec.Selector)
	if err != nil {
		return err
	}

	tweakListOptions := func(options metav1.ListOptions) metav1.ListOptions {
		options.LabelSelector = selector.String()
		return options
	}
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return client.Core().Pods(d.Namespace).List(tweakListOptions(options))
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return client.Core().Pods(d.Namespace).Watch(tweakListOptions(options))
		},
	}

	_, err = watchtools.UntilWithSync(d.Context, lw, &corev1.Pod{}, nil, func(e watch.Event) (bool, error) {
		if debug() {
			fmt.Printf("Deployment `%s` pods watcher: %#v\n", d.ResourceName, e.Type)
		}

		podObject, ok := e.Object.(*corev1.Pod)
		if !ok {
			return true, fmt.Errorf("Expected %s to be a *corev1.Pod, got %T", d.ResourceName, e.Object)
		}

		for _, pod := range d.MonitoredPods {
			if pod.ResourceName == podObject.Name {
				// Already under monitoring
				return false, nil
			}
		}

		pod := &PodWatchMonitor{
			WatchMonitor: WatchMonitor{
				Kube:         d.Kube,
				Namespace:    d.Namespace,
				ResourceName: podObject.Name,
			},

			ContainerLogChunk: d.ContainerLogChunk,

			ProcessedContainerLogTimestamps: make(map[string]time.Time),
		}

		d.MonitoredPods = append(d.MonitoredPods, pod)

		if debug() {
			fmt.Printf("Starting deployment's `%s` pod `%s` monitor\n", d.ResourceName, d.ResourceName)
		}

		go func() {
			err := pod.Watch()
			if err != nil {
				d.errors <- err
			}
		}()

		d.AddedPod <- pod

		return false, nil
	})

	return err
}

// DeploymentComplete considers a deployment to be complete once all of its desired replicas
// are updated and available, and no old pods are running.
func DeploymentComplete(deployment *extensions.Deployment, newStatus *extensions.DeploymentStatus) bool {
	return newStatus.UpdatedReplicas == *(deployment.Spec.Replicas) &&
		newStatus.Replicas == *(deployment.Spec.Replicas) &&
		newStatus.AvailableReplicas == *(deployment.Spec.Replicas) &&
		newStatus.ObservedGeneration >= deployment.Generation
}

// DeploymentProgressing reports progress for a deployment. Progress is estimated by comparing the
// current with the new status of the deployment that the controller is observing. More specifically,
// when new pods are scaled up or become available, or old pods are scaled down, then we consider the
// deployment is progressing.
func DeploymentProgressing(deployment *extensions.Deployment, newStatus *extensions.DeploymentStatus) bool {
	oldStatus := deployment.Status

	// Old replicas that need to be scaled down
	oldStatusOldReplicas := oldStatus.Replicas - oldStatus.UpdatedReplicas
	newStatusOldReplicas := newStatus.Replicas - newStatus.UpdatedReplicas

	return (newStatus.UpdatedReplicas > oldStatus.UpdatedReplicas) ||
		(newStatusOldReplicas < oldStatusOldReplicas) ||
		newStatus.AvailableReplicas > deployment.Status.AvailableReplicas
}

const TimedOutReason = "ProgressDeadlineExceeded"

var nowFn = func() time.Time { return time.Now() }

// DeploymentTimedOut considers a deployment to have timed out once its condition that reports progress
// is older than progressDeadlineSeconds or a Progressing condition with a TimedOutReason reason already
// exists.
func DeploymentTimedOut(deployment *extensions.Deployment, newStatus *extensions.DeploymentStatus) bool {
	if deployment.Spec.ProgressDeadlineSeconds == nil {
		return false
	}

	// Look for the Progressing condition. If it doesn't exist, we have no base to estimate progress.
	// If it's already set with a TimedOutReason reason, we have already timed out, no need to check
	// again.
	condition := GetDeploymentCondition(*newStatus, extensions.DeploymentProgressing)
	if condition == nil {
		return false
	}
	if condition.Reason == TimedOutReason {
		return true
	}

	// Look at the difference in seconds between now and the last time we reported any
	// progress or tried to create a replica set, or resumed a paused deployment and
	// compare against progressDeadlineSeconds.
	from := condition.LastUpdateTime
	now := nowFn()
	delta := time.Duration(*deployment.Spec.ProgressDeadlineSeconds) * time.Second
	timedOut := from.Add(delta).Before(now)

	if debug() {
		fmt.Printf("Deployment %q timed out (%t) [last progress check: %v - now: %v]", deployment.Name, timedOut, from, now)
	}
	//glog.V(4).Infof("Deployment %q timed out (%t) [last progress check: %v - now: %v]", deployment.Name, timedOut, from, now)
	return timedOut
}

// GetDeploymentCondition returns the condition with the provided type.
func GetDeploymentCondition(status extensions.DeploymentStatus, condType extensions.DeploymentConditionType) *extensions.DeploymentCondition {
	for i := range status.Conditions {
		c := status.Conditions[i]
		if c.Type == condType {
			return &c
		}
	}
	return nil
}

type rsListFunc func(string, metav1.ListOptions) ([]*extensions.ReplicaSet, error)

// rsListFromClient returns an rsListFunc that wraps the given client.
func rsListFromClient(c kubernetes.Interface) rsListFunc {
	return func(namespace string, options metav1.ListOptions) ([]*extensions.ReplicaSet, error) {
		rsList, err := c.Extensions().ReplicaSets(namespace).List(options)
		if err != nil {
			return nil, err
		}
		var ret []*extensions.ReplicaSet
		for i := range rsList.Items {
			ret = append(ret, &rsList.Items[i])
		}
		return ret, err
	}
}

// GetAllReplicaSets returns the old and new replica sets targeted by the given Deployment. It gets PodList and ReplicaSetList from client interface.
// Note that the first set of old replica sets doesn't include the ones with no pods, and the second set of old replica sets include all old replica sets.
// The third returned value is the new replica set, and it may be nil if it doesn't exist yet.
func GetAllReplicaSets(deployment *extensions.Deployment, c kubernetes.Interface) ([]*extensions.ReplicaSet, []*extensions.ReplicaSet, *extensions.ReplicaSet, error) {
	rsList, err := ListReplicaSets(deployment, rsListFromClient(c))
	if err != nil {
		return nil, nil, nil, err
	}
	oldRSes, allOldRSes, err := FindOldReplicaSets(deployment, rsList)
	if err != nil {
		return nil, nil, nil, err
	}
	newRS, err := FindNewReplicaSet(deployment, rsList)
	if err != nil {
		return nil, nil, nil, err
	}
	return oldRSes, allOldRSes, newRS, nil
}

// FindNewReplicaSet returns the new RS this given deployment targets (the one with the same pod template).
func FindNewReplicaSet(deployment *extensions.Deployment, rsList []*extensions.ReplicaSet) (*extensions.ReplicaSet, error) {
	newRSTemplate := GetNewReplicaSetTemplate(deployment)
	sort.Sort(ReplicaSetsByCreationTimestamp(rsList))
	for i := range rsList {
		if EqualIgnoreHash(rsList[i].Spec.Template, newRSTemplate) {
			// In rare cases, such as after cluster upgrades, Deployment may end up with
			// having more than one new ReplicaSets that have the same template as its template,
			// see https://github.com/kubernetes/kubernetes/issues/40415
			// We deterministically choose the oldest new ReplicaSet.
			return rsList[i], nil
		}
	}
	// new ReplicaSet does not exist.
	return nil, nil
}

// GetNewReplicaSetTemplate returns the desired PodTemplateSpec for the new ReplicaSet corresponding to the given ReplicaSet.
// Callers of this helper need to set the DefaultDeploymentUniqueLabelKey k/v pair.
func GetNewReplicaSetTemplate(deployment *extensions.Deployment) corev1.PodTemplateSpec {
	// newRS will have the same template as in deployment spec.
	return corev1.PodTemplateSpec{
		ObjectMeta: deployment.Spec.Template.ObjectMeta,
		Spec:       deployment.Spec.Template.Spec,
	}
}

// FindOldReplicaSets returns the old replica sets targeted by the given Deployment, with the given slice of RSes.
// Note that the first set of old replica sets doesn't include the ones with no pods, and the second set of old replica sets include all old replica sets.
func FindOldReplicaSets(deployment *extensions.Deployment, rsList []*extensions.ReplicaSet) ([]*extensions.ReplicaSet, []*extensions.ReplicaSet, error) {
	var requiredRSs []*extensions.ReplicaSet
	var allRSs []*extensions.ReplicaSet
	newRS, err := FindNewReplicaSet(deployment, rsList)
	if err != nil {
		return nil, nil, err
	}
	for _, rs := range rsList {
		// Filter out new replica set
		if newRS != nil && rs.UID == newRS.UID {
			continue
		}
		allRSs = append(allRSs, rs)
		if *(rs.Spec.Replicas) != 0 {
			requiredRSs = append(requiredRSs, rs)
		}
	}
	return requiredRSs, allRSs, nil
}

// ListReplicaSets returns a slice of RSes the given deployment targets.
// Note that this does NOT attempt to reconcile ControllerRef (adopt/orphan),
// because only the controller itself should do that.
// However, it does filter out anything whose ControllerRef doesn't match.
func ListReplicaSets(deployment *extensions.Deployment, getRSList rsListFunc) ([]*extensions.ReplicaSet, error) {
	// TODO: Right now we list replica sets by their labels. We should list them by selector, i.e. the replica set's selector
	//       should be a superset of the deployment's selector, see https://github.com/kubernetes/kubernetes/issues/19830.
	namespace := deployment.Namespace
	selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil {
		return nil, err
	}
	options := metav1.ListOptions{LabelSelector: selector.String()}
	all, err := getRSList(namespace, options)
	if err != nil {
		return all, err
	}
	// Only include those whose ControllerRef matches the Deployment.
	owned := make([]*extensions.ReplicaSet, 0, len(all))
	for _, rs := range all {
		controllerRef := GetControllerOf(rs)
		if controllerRef != nil && controllerRef.UID == deployment.UID {
			owned = append(owned, rs)
		}
	}
	return owned, nil
}

// EqualIgnoreHash returns true if two given podTemplateSpec are equal, ignoring the diff in value of Labels[pod-template-hash]
// We ignore pod-template-hash because the hash result would be different upon podTemplateSpec API changes
// (e.g. the addition of a new field will cause the hash code to change)
// Note that we assume input podTemplateSpecs contain non-empty labels
func EqualIgnoreHash(template1, template2 corev1.PodTemplateSpec) bool {
	// First, compare template.Labels (ignoring hash)
	labels1, labels2 := template1.Labels, template2.Labels
	if len(labels1) > len(labels2) {
		labels1, labels2 = labels2, labels1
	}
	// We make sure len(labels2) >= len(labels1)
	for k, v := range labels2 {
		if labels1[k] != v && k != extensions.DefaultDeploymentUniqueLabelKey {
			return false
		}
	}
	// Then, compare the templates without comparing their labels
	template1.Labels, template2.Labels = nil, nil
	return apiequality.Semantic.DeepEqual(template1, template2)
}

// ReplicaSetsByCreationTimestamp sorts a list of ReplicaSet by creation timestamp, using their names as a tie breaker.
type ReplicaSetsByCreationTimestamp []*extensions.ReplicaSet

func (o ReplicaSetsByCreationTimestamp) Len() int      { return len(o) }
func (o ReplicaSetsByCreationTimestamp) Swap(i, j int) { o[i], o[j] = o[j], o[i] }
func (o ReplicaSetsByCreationTimestamp) Less(i, j int) bool {
	if o[i].CreationTimestamp.Equal(&o[j].CreationTimestamp) {
		return o[i].Name < o[j].Name
	}
	return o[i].CreationTimestamp.Before(&o[j].CreationTimestamp)
}

// GetControllerOf returns the controllerRef if controllee has a controller,
// otherwise returns nil.
func GetControllerOf(controllee metav1.Object) *metav1.OwnerReference {
	ownerRefs := controllee.GetOwnerReferences()
	for i := range ownerRefs {
		owner := &ownerRefs[i]
		if owner.Controller != nil && *owner.Controller == true {
			return owner
		}
	}
	return nil
}
