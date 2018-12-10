package tracker

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"

	"github.com/flant/kubedog/pkg/utils"
)

// TrackStatefulSet is for monitor StatefulSet rollout
func TrackStatefulSet(name string, namespace string, kube kubernetes.Interface, feed ControllerFeed, opts Options) error {
	if debug() {
		fmt.Printf("> TrackStatefulSet\n")
	}

	errorChan := make(chan error, 0)
	doneChan := make(chan bool, 0)

	parentContext := opts.ParentContext
	if parentContext == nil {
		parentContext = context.Background()
	}
	ctx, cancel := watchtools.ContextWithOptionalTimeout(parentContext, opts.Timeout)
	defer cancel()

	StatefulSetTracker := NewStatefulSetTracker(ctx, name, namespace, kube, opts)

	go func() {
		if debug() {
			fmt.Printf("  goroutine: start statefulset/%s tracker\n", name)
		}
		err := StatefulSetTracker.Track()
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- true
		}
	}()

	if debug() {
		fmt.Printf("  statefulset/%s: for-select StatefulSetTracker channels\n", name)
	}

	for {
		select {
		case isReady := <-StatefulSetTracker.Added:
			if debug() {
				fmt.Printf("    statefulset/%s added\n", name)
			}

			err := feed.Added(isReady)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case <-StatefulSetTracker.Ready:
			if debug() {
				fmt.Printf("    statefulset/%s ready: desired: %d, current: %d, updated: %d, ready: %d\n",
					name,
					StatefulSetTracker.FinalStatefulSetStatus.Replicas,
					StatefulSetTracker.FinalStatefulSetStatus.CurrentReplicas,
					StatefulSetTracker.FinalStatefulSetStatus.UpdatedReplicas,
					StatefulSetTracker.FinalStatefulSetStatus.ReadyReplicas,
				)
			}

			err := feed.Ready()
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case reason := <-StatefulSetTracker.Failed:
			if debug() {
				fmt.Printf("    statefulset/%s failed. Tracker state: `%s`\n", name, StatefulSetTracker.State)
			}

			err := feed.Failed(reason)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case msg := <-StatefulSetTracker.EventMsg:
			if debug() {
				fmt.Printf("    statefulset/%s event: %s\n", name, msg)
			}

			err := feed.EventMsg(msg)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case pod := <-StatefulSetTracker.AddedPod:
			if debug() {
				fmt.Printf("    statefulset/%s got new pod `%s`\n", StatefulSetTracker.ResourceName, pod.Name)
			}

			err := feed.AddedPod(pod)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case chunk := <-StatefulSetTracker.PodLogChunk:
			if debug() {
				fmt.Printf("    statefulset/%s pod `%s` log chunk\n", StatefulSetTracker.ResourceName, chunk.PodName)
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

		case podError := <-StatefulSetTracker.PodError:
			if debug() {
				fmt.Printf("    statefulset/%s pod error: %s\n", StatefulSetTracker.ResourceName, podError.Message)
			}

			err := feed.PodError(podError)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case err := <-errorChan:
			return fmt.Errorf("statefulset/%s error: %v", name, err)
		case <-doneChan:
			return nil
		}
	}
}

// StatefulSetTracker ...
type StatefulSetTracker struct {
	Tracker
	LogsFromTime time.Time

	State                  string
	Conditions             []string
	FinalStatefulSetStatus appsv1.StatefulSetStatus
	lastObject             *appsv1.StatefulSet

	Added       chan bool
	Ready       chan bool
	Failed      chan string
	EventMsg    chan string
	AddedPod    chan ReplicaSetPod
	PodLogChunk chan *ReplicaSetPodLogChunk
	PodError    chan ReplicaSetPodError

	resourceAdded    chan *appsv1.StatefulSet
	resourceModified chan *appsv1.StatefulSet
	resourceDeleted  chan *appsv1.StatefulSet
	resourceFailed   chan string
	podAdded         chan *corev1.Pod
	podDone          chan string
	errors           chan error

	FailedReason chan error

	TrackedPods []string
}

// NewStatefulSetTracker ...
func NewStatefulSetTracker(ctx context.Context, name, namespace string, kube kubernetes.Interface, opts Options) *StatefulSetTracker {
	if debug() {
		fmt.Printf("> NewStatefulSetTracker\n")
	}
	return &StatefulSetTracker{
		Tracker: Tracker{
			Kube:             kube,
			Namespace:        namespace,
			FullResourceName: fmt.Sprintf("sts/%s", name),
			ResourceName:     name,
			Context:          ctx,
		},

		LogsFromTime: opts.LogsFromTime,

		Added:       make(chan bool, 0),
		Ready:       make(chan bool, 1),
		Failed:      make(chan string, 1),
		EventMsg:    make(chan string, 1),
		AddedPod:    make(chan ReplicaSetPod, 10),
		PodLogChunk: make(chan *ReplicaSetPodLogChunk, 1000),
		PodError:    make(chan ReplicaSetPodError, 0),

		TrackedPods: make([]string, 0),

		resourceAdded:    make(chan *appsv1.StatefulSet, 1),
		resourceModified: make(chan *appsv1.StatefulSet, 1),
		resourceDeleted:  make(chan *appsv1.StatefulSet, 1),
		resourceFailed:   make(chan string, 1),
		podAdded:         make(chan *corev1.Pod, 1),
		podDone:          make(chan string, 1),
		errors:           make(chan error, 0),
	}
}

// Track starts tracking of StatefulSet rollout process.
// watch only for one StatefulSet resource with name d.ResourceName within the namespace with name d.Namespace
// Watcher can wait for namespace creation and then for StatefulSet creation
// watcher receives added event if StatefulSet is started
// watch is infinite by default
// there is option StopOnAvailable — if true, watcher stops after StatefulSet has available status
// you can define custom stop triggers using custom implementation of ControllerFeed.
func (d *StatefulSetTracker) Track() (err error) {
	if debug() {
		fmt.Printf("> StatefulSetTracker.Track()\n")
	}

	d.runStatefulSetInformer()

	for {
		select {
		case object := <-d.resourceAdded:
			d.lastObject = object
			ready, err := d.handleStatefulSetState(object)
			if err != nil {
				if debug() {
					fmt.Printf("handle StatefulSet state error: %v", err)
				}
				return err
			}
			if debug() {
				fmt.Printf("StatefulSet `%s` initial ready state: %v\n", d.ResourceName, ready)
			}

			switch d.State {
			case "":
				d.State = "Started"
				d.Added <- ready
			}

			d.runPodsInformer()
			d.runEventsInformer()

		case object := <-d.resourceModified:
			d.lastObject = object
			ready, err := d.handleStatefulSetState(object)
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

		case pod := <-d.podAdded:
			if debug() {
				fmt.Printf("po/%s added\n", pod.Name)
			}

			rsPod := ReplicaSetPod{
				Name:       pod.Name,
				ReplicaSet: ReplicaSet{},
			}

			d.AddedPod <- rsPod

			err = d.runPodTracker(pod.Name)
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

// runStatefulSetInformer watch for StatefulSet events
func (d *StatefulSetTracker) runStatefulSetInformer() {
	client := d.Kube

	tweakListOptions := func(options metav1.ListOptions) metav1.ListOptions {
		options.FieldSelector = fields.OneTermEqualSelector("metadata.name", d.ResourceName).String()
		return options
	}
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return client.AppsV1().StatefulSets(d.Namespace).List(tweakListOptions(options))
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return client.AppsV1().StatefulSets(d.Namespace).Watch(tweakListOptions(options))
		},
	}

	go func() {
		_, err := watchtools.UntilWithSync(d.Context, lw, &appsv1.StatefulSet{}, nil, func(e watch.Event) (bool, error) {
			if debug() {
				fmt.Printf("    statefulset/%s event: %#v\n", d.ResourceName, e.Type)
			}

			var object *appsv1.StatefulSet

			if e.Type != watch.Error {
				var ok bool
				object, ok = e.Object.(*appsv1.StatefulSet)
				if !ok {
					return true, fmt.Errorf("expected %s to be a *appsv1.StatefulSet, got %T", d.ResourceName, e.Object)
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
				err := fmt.Errorf("StatefulSet error: %v", e.Object)
				//d.errors <- err
				return true, err
			}

			return false, nil
		})

		if err != nil {
			d.errors <- err
		}

		if debug() {
			fmt.Printf("      sts/%s informer DONE\n", d.ResourceName)
		}
	}()

	return
}

// runPodsInformer watch for StatefulSet Pods events
func (d *StatefulSetTracker) runPodsInformer() {
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

func (d *StatefulSetTracker) runPodTracker(podName string) error {
	errorChan := make(chan error, 0)
	doneChan := make(chan struct{}, 0)

	pod := NewPodTracker(d.Context, podName, d.Namespace, d.Kube)
	if !d.LogsFromTime.IsZero() {
		pod.LogsFromTime = d.LogsFromTime
	}
	d.TrackedPods = append(d.TrackedPods, podName)

	go func() {
		if debug() {
			fmt.Printf("Starting StatefulSet's `%s` Pod `%s` tracker. pod state: %v\n", d.ResourceName, pod.ResourceName, pod.State)
		}

		err := pod.Track()
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- struct{}{}
		}

		if debug() {
			fmt.Printf("Done StatefulSet's `%s` Pod `%s` tracker\n", d.ResourceName, pod.ResourceName)
		}
	}()

	go func() {
		for {
			select {
			case chunk := <-pod.ContainerLogChunk:
				rsChunk := &ReplicaSetPodLogChunk{
					PodLogChunk: &PodLogChunk{
						ContainerLogChunk: chunk,
						PodName:           pod.ResourceName,
					},
					ReplicaSet: ReplicaSet{},
				}

				d.PodLogChunk <- rsChunk
			case containerError := <-pod.ContainerError:
				podError := ReplicaSetPodError{
					PodError: PodError{
						ContainerError: containerError,
						PodName:        pod.ResourceName,
					},
					ReplicaSet: ReplicaSet{},
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

func (d *StatefulSetTracker) handleStatefulSetState(object *appsv1.StatefulSet) (ready bool, err error) {
	if debug() {
		fmt.Printf("%s\n", getStatefulSetStatus(object))
	}

	msg := ""
	msg, ready, err = StatefulSetRolloutStatus(object)

	if debug() {
		evList, err := utils.ListEventsForObject(d.Kube, object)
		if err != nil {
			return false, err
		}
		utils.DescribeEvents(evList)
	}

	if debug() {
		if ready {
			fmt.Printf("StatefulSet READY. %s\n", msg)
		} else {
			fmt.Printf("StatefulSet NOT READY. %s\n", msg)
		}
	}

	return ready, err
}

// runEventsInformer watch for StatefulSet events
func (d *StatefulSetTracker) runEventsInformer() {
	if d.lastObject == nil {
		return
	}

	eventInformer := NewEventInformer(d.Tracker, d.lastObject)
	eventInformer.WithChannels(d.EventMsg, d.resourceFailed, d.errors)
	eventInformer.Run()

	return
}
