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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"
	//deployutil "k8s.io/kubernetes/pkg/controller/deployment/util"
)

// DeploymentFeed interface for rollout process callbacks
type DeploymentFeed interface {
	Added() error
	Completed() error
	Failed(reason string) error
	AddedReplicaSet(rsName string) error
	AddedPod(podName string, rsName string, isNew bool) error
	PodLogChunk(*PodLogChunk) error
	PodError(PodError) error
}

// WatchDeploymentRollout is for monitor deployment rollout
func WatchDeploymentRollout(name string, namespace string, kube kubernetes.Interface, feed DeploymentFeed, opts WatchOptions) error {
	if debug() {
		fmt.Printf("> DeploymentRollout\n")
	}

	errorChan := make(chan error, 0)
	doneChan := make(chan bool, 0)

	parentContext := opts.ParentContext
	if parentContext == nil {
		parentContext = context.Background()
	}
	ctx, cancel := watchtools.ContextWithOptionalTimeout(parentContext, opts.Timeout)
	defer cancel()

	deploymentMon := NewDeploymentWatchMonitor(ctx, name, namespace, kube, opts)

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
		case <-deploymentMon.Added:
			if debug() {
				fmt.Printf("    deploy/%s added\n", name)
			}

			err := feed.Added()
			if err == StopWatch {
				return nil
			}
			if err != nil {
				return err
			}

		case <-deploymentMon.Completed:
			if debug() {
				fmt.Printf("    deploy/%s completed: desired: %d, current: %d/%d, up-to-date: %d, available: %d\n",
					name,
					deploymentMon.FinalDeploymentStatus.Replicas,
					deploymentMon.FinalDeploymentStatus.ReadyReplicas,
					deploymentMon.FinalDeploymentStatus.UnavailableReplicas,
					deploymentMon.FinalDeploymentStatus.UpdatedReplicas,
					deploymentMon.FinalDeploymentStatus.AvailableReplicas,
				)
			}

			err := feed.Completed()
			if err == StopWatch {
				return nil
			}
			if err != nil {
				return err
			}

		case reason := <-deploymentMon.Failed:
			if debug() {
				fmt.Printf("    deploy/%s failed. Monitor state: `%s`", name, deploymentMon.State)
			}

			err := feed.Failed(reason)
			if err == StopWatch {
				return nil
			}
			if err != nil {
				return err
			}

		case rsName := <-deploymentMon.AddedReplicaSet:
			if debug() {
				fmt.Printf("    deploy/%s got new replicaset `%s`\n", deploymentMon.ResourceName, rsName)
			}

			err := feed.AddedReplicaSet(rsName)
			if err == StopWatch {
				return nil
			}
			if err != nil {
				return err
			}

		case podName := <-deploymentMon.AddedPod:
			// TODO add replicaset name
			if debug() {
				fmt.Printf("    deploy/%s got new pod `%s`\n", deploymentMon.ResourceName, podName)
			}

			err := feed.AddedPod(podName, "", true)
			if err == StopWatch {
				return nil
			}
			if err != nil {
				return err
			}

		case chunk := <-deploymentMon.PodLogChunk:
			// TODO add replicaset name
			if debug() {
				fmt.Printf("    deploy/%s pod `%s` log chunk\n", deploymentMon.ResourceName, chunk.PodName)
				for _, line := range chunk.LogLines {
					fmt.Printf("po/%s [%s] %s\n", line.Timestamp, chunk.PodName, line.Data)
				}
			}

			err := feed.PodLogChunk(chunk)
			if err == StopWatch {
				return nil
			}
			if err != nil {
				return err
			}

		case podError := <-deploymentMon.PodError:
			if debug() {
				fmt.Printf("    deploy/%s pod error: %#v", deploymentMon.ResourceName, podError)
			}

			err := feed.PodError(podError)
			if err == StopWatch {
				return nil
			}
			if err != nil {
				return err
			}

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

	PreviousManifest *extensions.Deployment
	CurrentManifest  *extensions.Deployment
	CurrentComplete  bool

	State                 string
	Conditions            []string
	FinalDeploymentStatus extensions.DeploymentStatus

	Added           chan bool
	Completed       chan bool
	Failed          chan string
	AddedReplicaSet chan string
	AddedPod        chan string
	PodLogChunk     chan *PodLogChunk
	PodError        chan PodError

	resourceAdded    chan *extensions.Deployment
	resourceModified chan *extensions.Deployment
	resourceDeleted  chan *extensions.Deployment
	replicaSetAdded  chan *extensions.ReplicaSet
	podAdded         chan *corev1.Pod
	podDone          chan string
	errors           chan error

	FailedReason chan error

	MonitoredPods []string
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

		Added:           make(chan bool, 0),
		Completed:       make(chan bool, 1),
		Failed:          make(chan string, 1),
		AddedReplicaSet: make(chan string, 10),
		AddedPod:        make(chan string, 10),
		PodLogChunk:     make(chan *PodLogChunk, 1000),
		PodError:        make(chan PodError, 0),

		MonitoredPods: make([]string, 0),

		//PodError: make(chan PodError, 0),
		resourceAdded:    make(chan *extensions.Deployment, 0),
		resourceModified: make(chan *extensions.Deployment, 1),
		resourceDeleted:  make(chan *extensions.Deployment, 1),
		replicaSetAdded:  make(chan *extensions.ReplicaSet, 1),
		podAdded:         make(chan *corev1.Pod, 1),
		podDone:          make(chan string, 1),
		errors:           make(chan error, 0),
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

	go d.runDeploymentInformer()

	for {
		select {
		// getOrWait returns existed deployment or a new deployment over d.ResourceAvailable channel
		case object := <-d.resourceAdded:
			d.handleDeploymentState(object)

			switch d.State {
			case "":
				d.State = "Started"
				d.Added <- true
			}

			go d.runReplicaSetsInformer()
			go d.runPodsInformer()
			// go d.waitForNewReplicaSet()
		case object := <-d.resourceModified:
			complete, err := d.handleDeploymentState(object)
			if err != nil {
				return err
			}
			if complete {
				d.Completed <- true
				// ROLLOUT MODE
				return nil
			}
		case <-d.resourceDeleted:
			d.State = "Deleted"
			// TODO create DeploymentErrors!
			d.Failed <- "resource deleted"
		case rs := <-d.replicaSetAdded:
			if debug() {
				fmt.Printf("rs/%s added\n", rs.Name)
			}
			d.AddedReplicaSet <- rs.Name
		case pod := <-d.podAdded:
			if debug() {
				fmt.Printf("po/%s added\n", pod.Name)
			}
			d.AddedPod <- pod.Name

			err := d.runPodWatcher(pod.Name)
			if err != nil {
				return err
			}
		case podName := <-d.podDone:
			monitoredPods := make([]string, 0)
			for _, name := range d.MonitoredPods {
				if name != podName {
					monitoredPods = append(monitoredPods, name)
				}
			}
			d.MonitoredPods = monitoredPods

		case <-d.Context.Done():
			return ErrWatchTimeout
		case err := <-d.errors:
			return err
		}
	}

	return err
}

// runDeploymentInformer watch for deployment events
func (d *DeploymentWatchMonitor) runDeploymentInformer() {
	client := d.Kube

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
				fmt.Printf("    deploy/%s event: %#v\n", d.ResourceName, e.Type)
			}

			var object *extensions.Deployment

			if e.Type != watch.Error {
				var ok bool
				object, ok = e.Object.(*extensions.Deployment)
				if !ok {
					return true, fmt.Errorf("expected %s to be a *extension.Deployment, got %T", d.ResourceName, e.Object)
				}
			}

			switch e.Type {
			case watch.Added:
				d.resourceAdded <- object
			case watch.Modified:
				d.resourceModified <- object
			case watch.Deleted:
				d.resourceDeleted <- object
			}

			return false, nil
		})

		if err != nil {
			d.errors <- err
		}

		if debug() {
			fmt.Printf("      deploy/%s informer DONE\n", d.ResourceName)
		}
	}()

	return
}

// runDeploymentInformer watch for deployment events
func (d *DeploymentWatchMonitor) runReplicaSetsInformer() {
	client := d.Kube

	if d.CurrentManifest == nil {
		// This shouldn't happen!
		// TODO add error
		return
	}

	selector, err := metav1.LabelSelectorAsSelector(d.CurrentManifest.Spec.Selector)
	if err != nil {
		// TODO rescue this error!
		return
	}

	tweakListOptions := func(options metav1.ListOptions) metav1.ListOptions {
		options.LabelSelector = selector.String()
		return options
	}
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return client.Extensions().ReplicaSets(d.Namespace).List(tweakListOptions(options))
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return client.Extensions().ReplicaSets(d.Namespace).Watch(tweakListOptions(options))
		},
	}

	go func() {
		_, err := watchtools.UntilWithSync(d.Context, lw, &extensions.ReplicaSet{}, nil, func(e watch.Event) (bool, error) {
			if debug() {
				fmt.Printf("    deploy/%s replica set event: %#v\n", d.ResourceName, e.Type)
			}

			var object *extensions.ReplicaSet

			if e.Type != watch.Error {
				var ok bool
				object, ok = e.Object.(*extensions.ReplicaSet)
				if !ok {
					return true, fmt.Errorf("expected rs for %s to be a *extensions.ReplicaSet, got %T", d.ResourceName, e.Object)
				}
			}

			switch e.Type {
			case watch.Added:
				d.replicaSetAdded <- object
				// case watch.Modified:
				// 	d.resourceModified <- object
				// case watch.Deleted:
				// 	d.resourceDeleted <- object
			}

			return false, nil
		})

		if err != nil {
			d.errors <- err
		}

		if debug() {
			fmt.Printf("      deploy/%s new replicaSets informer DONE\n", d.ResourceName)
		}
	}()

	return
}

// runDeploymentInformer watch for deployment events
func (d *DeploymentWatchMonitor) runPodsInformer() {
	client := d.Kube

	if d.CurrentManifest == nil {
		// This shouldn't happen!
		// TODO add error
		return
	}

	selector, err := metav1.LabelSelectorAsSelector(d.CurrentManifest.Spec.Selector)
	if err != nil {
		// TODO rescue this error!
		return
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

	go func() {
		_, err := watchtools.UntilWithSync(d.Context, lw, &corev1.Pod{}, nil, func(e watch.Event) (bool, error) {
			if debug() {
				fmt.Printf("    deploy/%s pod event: %#v\n", d.ResourceName, e.Type)
			}

			var object *corev1.Pod

			if e.Type != watch.Error {
				var ok bool
				object, ok = e.Object.(*corev1.Pod)
				if !ok {
					return true, fmt.Errorf("expected %s to be a *extension.Deployment, got %T", d.ResourceName, e.Object)
				}
			}

			switch e.Type {
			case watch.Added:
				d.podAdded <- object
				// case watch.Modified:
				// 	d.resourceModified <- object
				// case watch.Deleted:
				// 	d.resourceDeleted <- object
			}

			return false, nil
		})

		if err != nil {
			d.errors <- err
		}

		if debug() {
			fmt.Printf("      deploy/%s new pods informer DONE\n", d.ResourceName)
		}
	}()

	return
}

func (d *DeploymentWatchMonitor) runPodWatcher(podName string) error {
	errorChan := make(chan error, 0)
	doneChan := make(chan struct{}, 0)

	pod := NewPodWatchMonitor(d.Context, podName, d.Namespace, d.Kube)
	d.MonitoredPods = append(d.MonitoredPods, podName)

	//job.AddedPod <- pod.ResourceName

	go func() {
		if debug() {
			fmt.Printf("Starting Deployment's `%s` Pod `%s` monitor\n", d.ResourceName, pod.ResourceName)
		}

		err := pod.Watch()
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- struct{}{}
		}

		if debug() {
			fmt.Printf("Done Deployment's `%s` Pod `%s` monitor\n", d.ResourceName, pod.ResourceName)
		}
	}()

	go func() {
		for {
			select {
			case chunk := <-pod.ContainerLogChunk:
				podChunk := &PodLogChunk{ContainerLogChunk: chunk, PodName: pod.ResourceName}
				d.PodLogChunk <- podChunk
			case containerError := <-pod.ContainerError:
				podError := PodError{ContainerError: containerError, PodName: pod.ResourceName}
				d.PodError <- podError
			case <-pod.Succeeded:
			case <-pod.Failed:
			case err := <-errorChan:
				d.errors <- err
				return
			case <-doneChan:
				d.podDone <- pod.ResourceName
				return
			}
		}
	}()

	return nil
}

func (d *DeploymentWatchMonitor) handleDeploymentState(object *extensions.Deployment) (completed bool, err error) {
	prevComplete := false
	// calc new status
	if d.CurrentManifest != nil {
		prevComplete = d.CurrentComplete
		newStatus := object.Status
		d.CurrentComplete = DeploymentComplete(d.CurrentManifest, &newStatus)
		d.PreviousManifest = d.CurrentManifest
	} else {
		d.CurrentComplete = false
	}
	d.CurrentManifest = object

	if prevComplete == false && d.CurrentComplete == true {
		completed = true
	}

	msgs := []string{}
	//msgs = append(msgs, fmt.Sprintf("    deploy/%s", d.ResourceName)) //\n      conditions:
	for _, c := range object.Status.Conditions {
		msgs = append(msgs, fmt.Sprintf("        - %s - %s - %s: \"%s\"", c.Type, c.Status, c.Reason, c.Message))
	}

	deployment := object
	if d.CurrentManifest != nil {
		deployment = d.CurrentManifest
	}
	if d.PreviousManifest != nil {
		deployment = d.PreviousManifest
	}
	msgs = append(msgs, fmt.Sprintf("        cpl: %v, prg: %v, tim: %v,    gn: %d, ogn: %d, des: %d, rdy: %d, upd: %d, avl: %d, uav: %d",
		yesNo(DeploymentComplete(deployment, &object.Status)),
		yesNo(DeploymentProgressing(deployment, &object.Status)),
		yesNo(DeploymentTimedOut(deployment, &object.Status)),
		object.Generation,
		object.Status.ObservedGeneration,
		object.Status.Replicas,
		object.Status.ReadyReplicas,
		object.Status.UpdatedReplicas,
		object.Status.AvailableReplicas,
		object.Status.UnavailableReplicas,
	))
	if debug() {
		fmt.Printf("%s\n", strings.Join(msgs, "\n"))
	}
	d.getReplicaSets()

	if completed && debug() {
		fmt.Printf("Deployment COMPLETE.\n")
	}

	return
}

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

func (d *DeploymentWatchMonitor) getReplicaSets() {
	client := d.Kube
	_, allOlds, newRs, err := GetAllReplicaSets(d.CurrentManifest, client)
	if err != nil {
		fmt.Printf("waitForNewReplicaSet error: %v\n", err)
	}
	for i, rs := range allOlds {
		fmt.Printf("        - old %2d: rs/%s gn: %d, ogn: %d, des: %d, rdy: %d, fll: %d, avl: %d\n",
			i, rs.Name,
			rs.Generation,
			rs.Status.ObservedGeneration,
			rs.Status.Replicas,
			rs.Status.ReadyReplicas,
			rs.Status.FullyLabeledReplicas,
			rs.Status.AvailableReplicas,
		)
	}
	if newRs != nil {
		fmt.Printf("        - new   : rs/%s gn: %d, ogn: %d, des: %d, rdy: %d, fll: %d, avl: %d\n",
			newRs.Name,
			newRs.Generation,
			newRs.Status.ObservedGeneration,
			newRs.Status.Replicas,
			newRs.Status.ReadyReplicas,
			newRs.Status.FullyLabeledReplicas,
			newRs.Status.AvailableReplicas,
		)
	}
}

// func (d *DeploymentWatchMonitor) watchPods() error {
// 	client := d.Kube

// 	deploymentManifest, err := client.AppsV1beta2().
// 		Deployments(d.Namespace).
// 		Get(d.ResourceName, metav1.GetOptions{})
// 	if err != nil {
// 		return fmt.Errorf("deployment `%s` in namespace `%s` get error: %v", d.ResourceName, d.Namespace, err)
// 	}
// 	selector, err := metav1.LabelSelectorAsSelector(deploymentManifest.Spec.Selector)
// 	if err != nil {
// 		return err
// 	}

// 	tweakListOptions := func(options metav1.ListOptions) metav1.ListOptions {
// 		options.LabelSelector = selector.String()
// 		return options
// 	}
// 	lw := &cache.ListWatch{
// 		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
// 			return client.Core().Pods(d.Namespace).List(tweakListOptions(options))
// 		},
// 		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
// 			return client.Core().Pods(d.Namespace).Watch(tweakListOptions(options))
// 		},
// 	}

// 	_, err = watchtools.UntilWithSync(d.Context, lw, &corev1.Pod{}, nil, func(e watch.Event) (bool, error) {
// 		if debug() {
// 			fmt.Printf("Deployment `%s` pods watcher: %#v\n", d.ResourceName, e.Type)
// 		}

// 		podObject, ok := e.Object.(*corev1.Pod)
// 		if !ok {
// 			return true, fmt.Errorf("Expected %s to be a *corev1.Pod, got %T", d.ResourceName, e.Object)
// 		}

// 		for _, pod := range d.MonitoredPods {
// 			if pod.ResourceName == podObject.Name {
// 				// Already under monitoring
// 				return false, nil
// 			}
// 		}

// 		pod := &PodWatchMonitor{
// 			WatchMonitor: WatchMonitor{
// 				Kube:         d.Kube,
// 				Namespace:    d.Namespace,
// 				ResourceName: podObject.Name,
// 			},

// 			ContainerLogChunk: d.ContainerLogChunk,

// 			ProcessedContainerLogTimestamps: make(map[string]time.Time),
// 		}

// 		d.MonitoredPods = append(d.MonitoredPods, pod)

// 		if debug() {
// 			fmt.Printf("Starting deployment's `%s` pod `%s` monitor\n", d.ResourceName, d.ResourceName)
// 		}

// 		go func() {
// 			err := pod.Watch()
// 			if err != nil {
// 				d.errors <- err
// 			}
// 		}()

// 		d.AddedPod <- pod

// 		return false, nil
// 	})

// 	return err
// }

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

func yesNo(v bool) string {
	if v {
		return "YES"
	} else {
		return " no"
	}
}

type podListFunc func(string, metav1.ListOptions) (*corev1.PodList, error)

// rsListFromClient returns an rsListFunc that wraps the given client.
func podListFromClient(c kubernetes.Interface) podListFunc {
	return func(namespace string, options metav1.ListOptions) (*corev1.PodList, error) {
		podList, err := c.CoreV1().Pods(namespace).List(options)
		if err != nil {
			return nil, err
		}
		return podList, nil
		//var ret []*corev1.Pod
		//for i := range podList.Items {
		//		ret = append(ret, &podList.Items[i])
		//	}
		//	return ret, err
	}
}

// c kubernetes.Interface) ([]*extensions.ReplicaSet, []*extensions.ReplicaSet, *extensions.ReplicaSet, error) {
// rsList, err := ListReplicaSets(deployment, rsListFromClient(c))
// if err != nil {
// 	return nil, nil, nil, err
// }

// ListPods returns a list of pods the given deployment targets.
// This needs a list of ReplicaSets for the Deployment,
// which can be found with ListReplicaSets().
// Note that this does NOT attempt to reconcile ControllerRef (adopt/orphan),
// because only the controller itself should do that.
// However, it does filter out anything whose ControllerRef doesn't match.
func ListPods(deployment *extensions.Deployment, rsList []*extensions.ReplicaSet, getPodList podListFunc) (*corev1.PodList, error) {
	namespace := deployment.Namespace
	selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil {
		return nil, err
	}
	options := metav1.ListOptions{LabelSelector: selector.String()}
	all, err := getPodList(namespace, options)
	if err != nil {
		return all, err
	}
	// Only include those whose ControllerRef points to a ReplicaSet that is in
	// turn owned by this Deployment.
	rsMap := make(map[types.UID]bool, len(rsList))
	for _, rs := range rsList {
		rsMap[rs.UID] = true
	}
	owned := &corev1.PodList{Items: make([]corev1.Pod, 0, len(all.Items))}
	for i := range all.Items {
		pod := &all.Items[i]
		controllerRef := metav1.GetControllerOf(pod)
		if controllerRef != nil && rsMap[controllerRef.UID] {
			owned.Items = append(owned.Items, *pod)
		}
	}
	return owned, nil
}

// 			// get all RSes
// 			_, allOlds, newRs, err := GetAllReplicaSets(d.CurrentManifest, client)

// 			// get all pods for RSes
// 			//               rs name    pod name
// 			oldPods := make(map[string]corev1.Pod)

// 			//get all pods
// 			podList, err := ListPods(object, allOlds, podListFromClient(client))
// 			if err != nil {
// 				return true, err
// 			}

// 			for _, pod := range podList.Items {
// 				oldPods[pod.Name] = pod
// 			}

// 			newPods := make(map[string]corev1.Pod)
// 			if newRs != nil {
// 				rss := make([]*extensions.ReplicaSet, 1)
// 				rss[0] = newRs

// 				podList, err := ListPods(object, rss, podListFromClient(client))
// 				if err != nil {
// 					return true, err
// 				}

// 				for _, pod := range podList.Items {
// 					newPods[pod.Name] = pod
// 				}
// 			}

// 			// if debug() {
// 			// 	// All Ready, hasPending, Fails
// 			// 	for _, pod := range newPods {
// 			// 		fmt.Printf("        po/%s %s\n", pod.Name, pod.Status.Phase)
// 			// 		for _, cs := range pod.Status.ContainerStatuses {
// 			// 			cs.State.
// 			// 		}
// 			// 	}
// 			// }
