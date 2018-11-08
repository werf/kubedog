package tracker

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	extensions "k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"

	"github.com/flant/kubedog/pkg/utils"
)

// DeploymentFeed interface for rollout process callbacks
type DeploymentFeed interface {
	Added(ready bool) error
	Ready() error
	Failed(reason string) error
	AddedReplicaSet(rsName string) error
	AddedPod(podName string, rsName string, isNew bool) error
	PodLogChunk(*PodLogChunk) error
	PodError(PodError) error
}

type ReplicaSetPod struct {
	Name   string
	RsName string
	RsNew  bool
}

// TrackDeployment is for monitor deployment rollout
func TrackDeployment(name string, namespace string, kube kubernetes.Interface, feed DeploymentFeed, opts Options) error {
	if debug() {
		fmt.Printf("> TrackDeployment\n")
	}

	errorChan := make(chan error, 0)
	doneChan := make(chan bool, 0)

	parentContext := opts.ParentContext
	if parentContext == nil {
		parentContext = context.Background()
	}
	ctx, cancel := watchtools.ContextWithOptionalTimeout(parentContext, opts.Timeout)
	defer cancel()

	deploymentTracker := NewDeploymentTracker(ctx, name, namespace, kube, opts)

	go func() {
		fmt.Printf("  goroutine: start deploy/%s tracker\n", name)
		err := deploymentTracker.Track()
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- true
		}
	}()

	if debug() {
		fmt.Printf("  deploy/%s: for-select DeploymentTracker channels\n", name)
	}

	for {
		select {
		case isReady := <-deploymentTracker.Added:
			if debug() {
				fmt.Printf("    deploy/%s added\n", name)
			}

			err := feed.Added(isReady)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case <-deploymentTracker.Ready:
			if debug() {
				fmt.Printf("    deploy/%s ready: desired: %d, current: %d/%d, up-to-date: %d, available: %d\n",
					name,
					deploymentTracker.FinalDeploymentStatus.Replicas,
					deploymentTracker.FinalDeploymentStatus.ReadyReplicas,
					deploymentTracker.FinalDeploymentStatus.UnavailableReplicas,
					deploymentTracker.FinalDeploymentStatus.UpdatedReplicas,
					deploymentTracker.FinalDeploymentStatus.AvailableReplicas,
				)
			}

			err := feed.Ready()
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case reason := <-deploymentTracker.Failed:
			if debug() {
				fmt.Printf("    deploy/%s failed. Tracker state: `%s`", name, deploymentTracker.State)
			}

			err := feed.Failed(reason)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case rsName := <-deploymentTracker.AddedReplicaSet:
			if debug() {
				fmt.Printf("    deploy/%s got new replicaset `%s`\n", deploymentTracker.ResourceName, rsName)
			}

			err := feed.AddedReplicaSet(rsName)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case rsPod := <-deploymentTracker.AddedPod:
			if debug() {
				fmt.Printf("    deploy/%s got new pod `%s`\n", deploymentTracker.ResourceName, rsPod.Name)
			}

			err := feed.AddedPod(rsPod.Name, rsPod.RsName, rsPod.RsNew)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case chunk := <-deploymentTracker.PodLogChunk:
			if debug() {
				fmt.Printf("    deploy/%s pod `%s` log chunk\n", deploymentTracker.ResourceName, chunk.PodName)
				for _, line := range chunk.LogLines {
					fmt.Printf("po/%s [%s] %s\n", line.Timestamp, chunk.PodName, line.Data)
				}
			}

			err := feed.PodLogChunk(chunk)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case podError := <-deploymentTracker.PodError:
			if debug() {
				fmt.Printf("    deploy/%s pod error: %#v", deploymentTracker.ResourceName, podError)
			}

			err := feed.PodError(podError)
			if err == StopTrack {
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

// DeploymentTracker ...
type DeploymentTracker struct {
	Tracker

	PreviousManifest *extensions.Deployment
	CurrentManifest  *extensions.Deployment
	CurrentReady     bool

	State                 string
	Conditions            []string
	FinalDeploymentStatus extensions.DeploymentStatus
	NewReplicaSetName     string

	Added           chan bool
	Ready           chan bool
	Failed          chan string
	AddedReplicaSet chan string
	AddedPod        chan ReplicaSetPod
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

	TrackedPods []string
}

// NewDeploymentTracker ...
func NewDeploymentTracker(ctx context.Context, name, namespace string, kube kubernetes.Interface, opts Options) *DeploymentTracker {
	if debug() {
		fmt.Printf("> NewDeploymentTracker\n")
	}
	return &DeploymentTracker{
		Tracker: Tracker{
			Kube:         kube,
			Namespace:    namespace,
			ResourceName: name,
			Context:      ctx,
		},

		Added:           make(chan bool, 0),
		Ready:           make(chan bool, 1),
		Failed:          make(chan string, 1),
		AddedReplicaSet: make(chan string, 10),
		AddedPod:        make(chan ReplicaSetPod, 10),
		PodLogChunk:     make(chan *PodLogChunk, 1000),
		PodError:        make(chan PodError, 0),

		TrackedPods: make([]string, 0),

		//PodError: make(chan PodError, 0),
		resourceAdded:    make(chan *extensions.Deployment, 1),
		resourceModified: make(chan *extensions.Deployment, 1),
		resourceDeleted:  make(chan *extensions.Deployment, 1),
		replicaSetAdded:  make(chan *extensions.ReplicaSet, 1),
		podAdded:         make(chan *corev1.Pod, 1),
		podDone:          make(chan string, 1),
		errors:           make(chan error, 0),
	}
}

// Track for deployment rollout process
// watch only for one deployment resource with name d.ResourceName within the namespace with name d.Namespace
// Watcher can wait for namespace creation and then for deployment creation
// watcher receives added event if deployment is started
// watch is infinite by default
// there is option StopOnAvailable — if true, watcher stops after deployment has available status
// you can define custom stop triggers using custom DeploymentFeed.
func (d *DeploymentTracker) Track() (err error) {
	if debug() {
		fmt.Printf("> DeploymentTracker.Track()\n")
	}

	d.runDeploymentInformer()

	for {
		select {
		// getOrWait returns existed deployment or a new deployment over d.ResourceAvailable channel
		case object := <-d.resourceAdded:
			ready, err := d.handleDeploymentState(object)
			if err != nil {
				fmt.Printf("handle deployment state error: %v", err)
				return err
			}
			if debug() {
				fmt.Printf("deployment `%s` initial ready state: %v\n", d.ResourceName, ready)
			}

			switch d.State {
			case "":
				d.State = "Started"
				d.Added <- ready
			}

			d.runReplicaSetsInformer()
			d.runPodsInformer()

		case object := <-d.resourceModified:
			ready, err := d.handleDeploymentState(object)
			if err != nil {
				return err
			}
			if ready {
				d.Ready <- true
				// ROLLOUT mode: tracker stops just after deployment set to ready.
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
			d.NewReplicaSetName = rs.Name
			d.AddedReplicaSet <- rs.Name
		case pod := <-d.podAdded:
			if debug() {
				fmt.Printf("po/%s added\n", pod.Name)
			}

			rsName := ""
			for _, ref := range pod.OwnerReferences {
				if ref.Kind == "ReplicaSet" {
					rsName = ref.Name
				}
			}

			rsNew := rsName == d.NewReplicaSetName

			rsPod := ReplicaSetPod{
				Name:   pod.Name,
				RsName: rsName,
				RsNew: rsNew,
			}

			d.AddedPod <- rsPod

			err := d.runPodTracker(pod.Name)
			if err != nil {
				return err
			}
		case podName := <-d.podDone:
			trackedPods := make([]string, 0)
			for _, name := range d.TrackedPods {
				if name != podName {
					trackedPods = append(trackedPods, name)
				}
			}
			d.TrackedPods = trackedPods

		case <-d.Context.Done():
			return ErrTrackTimeout
		case err := <-d.errors:
			return err
		}
	}

	return err
}

// runDeploymentInformer watch for deployment events
func (d *DeploymentTracker) runDeploymentInformer() {
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
func (d *DeploymentTracker) runReplicaSetsInformer() {
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
func (d *DeploymentTracker) runPodsInformer() {
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

func (d *DeploymentTracker) runPodTracker(podName string) error {
	errorChan := make(chan error, 0)
	doneChan := make(chan struct{}, 0)

	pod := NewPodTracker(d.Context, podName, d.Namespace, d.Kube)
	d.TrackedPods = append(d.TrackedPods, podName)

	go func() {
		if debug() {
			fmt.Printf("Starting Deployment's `%s` Pod `%s` tracker\n", d.ResourceName, pod.ResourceName)
		}

		err := pod.Track()
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- struct{}{}
		}

		if debug() {
			fmt.Printf("Done Deployment's `%s` Pod `%s` tracker\n", d.ResourceName, pod.ResourceName)
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
			case <-pod.Ready:
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

func (d *DeploymentTracker) handleDeploymentState(object *extensions.Deployment) (ready bool, err error) {
	if debug() {
		fmt.Printf("%s\n%s\n", getDeploymentStatus(d.Kube, d.CurrentManifest, object), getReplicaSetsStatus(d.Kube, object))
	}

	prevReady := false
	newStatus := object.Status
	// calc new status
	if d.CurrentManifest != nil {
		prevReady = d.CurrentReady
		d.CurrentReady = utils.DeploymentComplete(d.CurrentManifest, &newStatus)
		d.PreviousManifest = d.CurrentManifest
	} else {
		d.CurrentReady = utils.DeploymentComplete(object, &newStatus)
	}
	d.CurrentManifest = object

	if prevReady == false && d.CurrentReady == true {
		ready = true
	}

	if ready && debug() {
		fmt.Printf("Deployment READY.\n")
	}

	return
}
