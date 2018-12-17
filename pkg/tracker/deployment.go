package tracker

import (
	"context"
	"fmt"
	"time"

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

// TrackDeployment is for monitor deployment rollout
func TrackDeployment(name string, namespace string, kube kubernetes.Interface, feed ControllerFeed, opts Options) error {
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
		if debug() {
			fmt.Printf("  goroutine: start deploy/%s tracker\n", name)
		}
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

		case msg := <-deploymentTracker.EventMsg:
			if debug() {
				fmt.Printf("    deploy/%s event: %s", name, msg)
			}

			err := feed.EventMsg(msg)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case rs := <-deploymentTracker.AddedReplicaSet:
			if debug() {
				fmt.Printf("    deploy/%s got new replicaset `%s` (is new: %v)\n", deploymentTracker.ResourceName, rs.Name, rs.IsNew)
			}

			err := feed.AddedReplicaSet(rs)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case pod := <-deploymentTracker.AddedPod:
			if debug() {
				fmt.Printf("    deploy/%s got new pod `%s`\n", deploymentTracker.ResourceName, pod.Name)
			}

			err := feed.AddedPod(pod)
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
					fmt.Printf("po/%s [%s] %s\n", chunk.PodName, line.Timestamp, line.Message)
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
				fmt.Printf("    deploy/%s pod error: %s\n", deploymentTracker.ResourceName, podError.Message)
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
	LogsFromTime time.Time

	CurrentReady bool

	State                 string
	Conditions            []string
	FinalDeploymentStatus extensions.DeploymentStatus
	NewReplicaSetName     string
	knownReplicaSets      map[string]*extensions.ReplicaSet
	lastObject            *extensions.Deployment

	Added           chan bool
	Ready           chan bool
	Failed          chan string
	EventMsg        chan string
	AddedReplicaSet chan ReplicaSet
	AddedPod        chan ReplicaSetPod
	PodLogChunk     chan *ReplicaSetPodLogChunk
	PodError        chan ReplicaSetPodError

	resourceAdded      chan *extensions.Deployment
	resourceModified   chan *extensions.Deployment
	resourceDeleted    chan *extensions.Deployment
	resourceFailed     chan string
	replicaSetAdded    chan *extensions.ReplicaSet
	replicaSetModified chan *extensions.ReplicaSet
	replicaSetDeleted  chan *extensions.ReplicaSet
	podAdded           chan *corev1.Pod
	podDone            chan string
	errors             chan error

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
			Kube:             kube,
			Namespace:        namespace,
			FullResourceName: fmt.Sprintf("deploy/%s", name),
			ResourceName:     name,
			Context:          ctx,
		},

		LogsFromTime: opts.LogsFromTime,

		Added:           make(chan bool, 0),
		Ready:           make(chan bool, 1),
		Failed:          make(chan string, 1),
		EventMsg:        make(chan string, 1),
		AddedReplicaSet: make(chan ReplicaSet, 10),
		AddedPod:        make(chan ReplicaSetPod, 10),
		PodLogChunk:     make(chan *ReplicaSetPodLogChunk, 1000),
		PodError:        make(chan ReplicaSetPodError, 0),

		knownReplicaSets: make(map[string]*extensions.ReplicaSet),
		TrackedPods:      make([]string, 0),

		//PodError: make(chan PodError, 0),
		resourceAdded:      make(chan *extensions.Deployment, 1),
		resourceModified:   make(chan *extensions.Deployment, 1),
		resourceDeleted:    make(chan *extensions.Deployment, 1),
		resourceFailed:     make(chan string, 1),
		replicaSetAdded:    make(chan *extensions.ReplicaSet, 1),
		replicaSetModified: make(chan *extensions.ReplicaSet, 1),
		replicaSetDeleted:  make(chan *extensions.ReplicaSet, 1),
		podAdded:           make(chan *corev1.Pod, 1),
		podDone:            make(chan string, 1),
		errors:             make(chan error, 0),
	}
}

// Track starts tracking of deployment rollout process.
// watch only for one deployment resource with name d.ResourceName within the namespace with name d.Namespace
// Watcher can wait for namespace creation and then for deployment creation
// watcher receives added event if deployment is started
// watch is infinite by default
// there is option StopOnAvailable — if true, watcher stops after deployment has available status
// you can define custom stop triggers using custom implementation of ControllerFeed.
func (d *DeploymentTracker) Track() (err error) {
	if debug() {
		fmt.Printf("> DeploymentTracker.Track()\n")
	}

	d.runDeploymentInformer()

	for {
		select {
		case object := <-d.resourceAdded:
			ready, err := d.handleDeploymentState(object)
			if err != nil {
				if debug() {
					fmt.Printf("handle deployment state error: %v", err)
				}
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
			d.runEventsInformer()

		case object := <-d.resourceModified:
			ready, err := d.handleDeploymentState(object)
			if err != nil {
				return err
			}
			if ready {
				d.Ready <- true
			}
		case object := <-d.resourceDeleted:
			d.lastObject = object
			d.State = "Deleted"
			d.Failed <- "resource deleted"

		case reason := <-d.resourceFailed:
			d.State = "Failed"
			d.Failed <- reason

		case rs := <-d.replicaSetAdded:
			if debug() {
				fmt.Printf("rs/%s added\n", rs.Name)
			}

			d.knownReplicaSets[rs.Name] = rs

			rsNew, err := utils.IsReplicaSetNew(d.lastObject, d.knownReplicaSets, rs.Name)
			if err != nil {
				return err
			}

			d.AddedReplicaSet <- ReplicaSet{
				Name:  rs.Name,
				IsNew: rsNew,
			}

		case rs := <-d.replicaSetModified:
			if debug() {
				fmt.Printf("rs/%s modified\n", rs.Name)
			}

			d.knownReplicaSets[rs.Name] = rs

		case rs := <-d.replicaSetDeleted:
			delete(d.knownReplicaSets, rs.Name)

		case pod := <-d.podAdded:
			if debug() {
				fmt.Printf("po/%s added\n", pod.Name)
			}

			rsName := utils.GetPodReplicaSetName(pod)
			rsNew, err := utils.IsReplicaSetNew(d.lastObject, d.knownReplicaSets, rsName)
			if err != nil {
				return err
			}

			rsPod := ReplicaSetPod{
				Name: pod.Name,
				ReplicaSet: ReplicaSet{
					Name:  rsName,
					IsNew: rsNew,
				},
			}

			d.AddedPod <- rsPod

			err = d.runPodTracker(pod.Name, rsName)
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
			case watch.Error:
				err := fmt.Errorf("deployment error: %v", e.Object)
				//d.errors <- err
				return true, err
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

// runReplicaSetsInformer watch for deployment events
func (d *DeploymentTracker) runReplicaSetsInformer() {
	if d.lastObject == nil {
		// This shouldn't happen!
		// TODO add error
		return
	}

	rsInformer := NewReplicaSetInformer(d.Tracker, utils.ControllerAccessor(d.lastObject))
	rsInformer.WithChannels(d.replicaSetAdded, d.replicaSetModified, d.replicaSetDeleted, d.errors)
	rsInformer.Run()

	return
}

// runDeploymentInformer watch for deployment events
func (d *DeploymentTracker) runPodsInformer() {
	if d.lastObject == nil {
		// This shouldn't happen!
		// TODO add error
		return
	}

	podsInformer := NewPodsInformer(d.Tracker, utils.ControllerAccessor(d.lastObject))
	podsInformer.WithChannels(d.podAdded, d.errors)
	podsInformer.Run()

	return
}

func (d *DeploymentTracker) runPodTracker(podName, rsName string) error {
	errorChan := make(chan error, 0)
	doneChan := make(chan struct{}, 0)

	pod := NewPodTracker(d.Context, podName, d.Namespace, d.Kube)
	if !d.LogsFromTime.IsZero() {
		pod.LogsFromTime = d.LogsFromTime
	}
	d.TrackedPods = append(d.TrackedPods, podName)

	go func() {
		if debug() {
			fmt.Printf("Starting Deployment's `%s` Pod `%s` tracker. pod state: %v\n", d.ResourceName, pod.ResourceName, pod.State)
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
				rsNew, err := utils.IsReplicaSetNew(d.lastObject, d.knownReplicaSets, rsName)
				if err != nil {
					d.errors <- err
					return
				}

				rsChunk := &ReplicaSetPodLogChunk{
					PodLogChunk: &PodLogChunk{
						ContainerLogChunk: chunk,
						PodName:           pod.ResourceName,
					},
					ReplicaSet: ReplicaSet{
						Name:  rsName,
						IsNew: rsNew,
					},
				}

				d.PodLogChunk <- rsChunk
			case containerError := <-pod.ContainerError:
				rsNew, err := utils.IsReplicaSetNew(d.lastObject, d.knownReplicaSets, rsName)
				if err != nil {
					d.errors <- err
					return
				}

				podError := ReplicaSetPodError{
					PodError: PodError{
						ContainerError: containerError,
						PodName:        pod.ResourceName,
					},
					ReplicaSet: ReplicaSet{
						Name:  rsName,
						IsNew: rsNew,
					},
				}

				d.PodError <- podError
			case msg := <-pod.EventMsg:
				d.EventMsg <- fmt.Sprintf("po/%s %s", pod.ResourceName, msg)
			case <-pod.Added:
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

// TODO get rid of previous object
func (d *DeploymentTracker) handleDeploymentState(object *extensions.Deployment) (ready bool, err error) {
	if debug() {
		fmt.Printf("%s\n%s\n",
			getDeploymentStatus(d.Kube, d.lastObject, object),
			getReplicaSetsStatus(d.Kube, object))
	}

	if debug() {
		evList, err := utils.ListEventsForObject(d.Kube, object)
		if err != nil {
			return false, err
		}
		utils.DescribeEvents(evList)
	}

	prevReady := false
	newStatus := object.Status
	// calc new status
	if d.lastObject != nil {
		prevReady = d.CurrentReady
		d.CurrentReady = utils.DeploymentComplete(d.lastObject, &newStatus)
	} else {
		d.CurrentReady = utils.DeploymentComplete(object, &newStatus)
	}
	d.lastObject = object

	if prevReady == false && d.CurrentReady == true {
		ready = true
	}

	if ready && debug() {
		fmt.Printf("Deployment READY.\n")
	}

	return
}

// runEventsInformer watch for StatefulSet events
func (d *DeploymentTracker) runEventsInformer() {
	if d.lastObject == nil {
		return
	}

	eventInformer := NewEventInformer(d.Tracker, d.lastObject)
	eventInformer.WithChannels(d.EventMsg, d.resourceFailed, d.errors)
	eventInformer.Run()

	return
}
