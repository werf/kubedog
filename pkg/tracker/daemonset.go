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

// TrackDaemonSet is for monitor DaemonSet rollout
func TrackDaemonSet(name string, namespace string, kube kubernetes.Interface, feed ControllerFeed, opts Options) error {
	if debug() {
		fmt.Printf("> TrackDaemonSet\n")
	}

	errorChan := make(chan error, 0)
	doneChan := make(chan bool, 0)

	parentContext := opts.ParentContext
	if parentContext == nil {
		parentContext = context.Background()
	}
	ctx, cancel := watchtools.ContextWithOptionalTimeout(parentContext, opts.Timeout)
	defer cancel()

	DaemonSetTracker := NewDaemonSetTracker(ctx, name, namespace, kube, opts)

	go func() {
		if debug() {
			fmt.Printf("  goroutine: start Daemonset/%s tracker\n", name)
		}
		err := DaemonSetTracker.Track()
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- true
		}
	}()

	if debug() {
		fmt.Printf("  ds/%s: for-select DaemonSetTracker channels\n", name)
	}

	for {
		select {
		case isReady := <-DaemonSetTracker.Added:
			if debug() {
				fmt.Printf("    ds/%s added\n", name)
			}

			err := feed.Added(isReady)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case <-DaemonSetTracker.Ready:
			if debug() {
				fmt.Printf("    ds/%s ready: desired: %d, current: %d, updated: %d, ready: %d\n",
					name,
					DaemonSetTracker.FinalDaemonSetStatus.DesiredNumberScheduled,
					DaemonSetTracker.FinalDaemonSetStatus.CurrentNumberScheduled,
					DaemonSetTracker.FinalDaemonSetStatus.UpdatedNumberScheduled,
					DaemonSetTracker.FinalDaemonSetStatus.NumberReady,
				)
			}

			err := feed.Ready()
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case reason := <-DaemonSetTracker.Failed:
			if debug() {
				fmt.Printf("    ds/%s failed. Tracker state: `%s`", name, DaemonSetTracker.State)
			}

			err := feed.Failed(reason)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case msg := <-DaemonSetTracker.EventMsg:
			if debug() {
				fmt.Printf("    ds/%s event: %s\n", name, msg)
			}

			err := feed.EventMsg(msg)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case pod := <-DaemonSetTracker.AddedPod:
			if debug() {
				fmt.Printf("    ds/%s po/%s added\n", DaemonSetTracker.ResourceName, pod.Name)
			}

			err := feed.AddedPod(pod)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case chunk := <-DaemonSetTracker.PodLogChunk:
			if debug() {
				fmt.Printf("    ds/%s po/%s log chunk\n", DaemonSetTracker.ResourceName, chunk.PodName)
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

		case podError := <-DaemonSetTracker.PodError:
			if debug() {
				fmt.Printf("    ds/%s pod error: %s\n", DaemonSetTracker.ResourceName, podError.Message)
			}

			err := feed.PodError(podError)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case err := <-errorChan:
			return fmt.Errorf("ds/%s error: %v", name, err)
		case <-doneChan:
			return nil
		}
	}
}

// DaemonSetTracker ...
type DaemonSetTracker struct {
	Tracker
	LogsFromTime time.Time

	State                string
	Conditions           []string
	FinalDaemonSetStatus extensions.DaemonSetStatus
	lastObject           *extensions.DaemonSet

	Added       chan bool
	Ready       chan bool
	Failed      chan string
	EventMsg    chan string
	AddedPod    chan ReplicaSetPod
	PodLogChunk chan *ReplicaSetPodLogChunk
	PodError    chan ReplicaSetPodError

	resourceAdded    chan *extensions.DaemonSet
	resourceModified chan *extensions.DaemonSet
	resourceDeleted  chan *extensions.DaemonSet
	resourceFailed   chan string
	podAdded         chan *corev1.Pod
	podDone          chan string
	errors           chan error

	FailedReason chan error

	TrackedPods []string
}

// NewDaemonSetTracker ...
func NewDaemonSetTracker(ctx context.Context, name, namespace string, kube kubernetes.Interface, opts Options) *DaemonSetTracker {
	if debug() {
		fmt.Printf("> NewDaemonSetTracker\n")
	}
	return &DaemonSetTracker{
		Tracker: Tracker{
			Kube:             kube,
			Namespace:        namespace,
			FullResourceName: fmt.Sprintf("ds/%s", name),
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

		resourceAdded:    make(chan *extensions.DaemonSet, 1),
		resourceModified: make(chan *extensions.DaemonSet, 1),
		resourceDeleted:  make(chan *extensions.DaemonSet, 1),
		resourceFailed:   make(chan string, 1),
		podAdded:         make(chan *corev1.Pod, 1),
		podDone:          make(chan string, 1),
		errors:           make(chan error, 0),
	}
}

// Track starts tracking of DaemonSet rollout process.
// watch only for one DaemonSet resource with name d.ResourceName within the namespace with name d.Namespace
// Watcher can wait for namespace creation and then for DaemonSet creation
// watcher receives added event if DaemonSet is started
// watch is infinite by default
// there is option StopOnAvailable — if true, watcher stops after DaemonSet has available status
// you can define custom stop triggers using custom implementation of ControllerFeed.
func (d *DaemonSetTracker) Track() (err error) {
	if debug() {
		fmt.Printf("> DaemonSetTracker.Track()\n")
	}

	d.runDaemonSetInformer()

	for {
		select {
		case object := <-d.resourceAdded:
			d.lastObject = object
			ready, err := d.handleDaemonSetStatus(object)
			if err != nil {
				if debug() {
					fmt.Printf("handle DaemonSet state error: %v", err)
				}
				return err
			}
			if debug() {
				fmt.Printf("DaemonSet `%s` initial ready state: %v\n", d.ResourceName, ready)
			}

			switch d.State {
			case "":
				d.State = "Started"
				d.Added <- ready
			}

			d.runPodsInformer()
			d.runEventsInformer()

		case object := <-d.resourceModified:
			ready, err := d.handleDaemonSetStatus(object)
			if err != nil {
				return err
			}
			d.lastObject = object
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

// runDaemonSetInformer watch for DaemonSet events
func (d *DaemonSetTracker) runDaemonSetInformer() {
	client := d.Kube

	tweakListOptions := func(options metav1.ListOptions) metav1.ListOptions {
		options.FieldSelector = fields.OneTermEqualSelector("metadata.name", d.ResourceName).String()
		return options
	}
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return client.ExtensionsV1beta1().DaemonSets(d.Namespace).List(tweakListOptions(options))
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return client.ExtensionsV1beta1().DaemonSets(d.Namespace).Watch(tweakListOptions(options))
		},
	}

	go func() {
		_, err := watchtools.UntilWithSync(d.Context, lw, &extensions.DaemonSet{}, nil, func(e watch.Event) (bool, error) {
			if debug() {
				fmt.Printf("    Daemonset/%s event: %#v\n", d.ResourceName, e.Type)
			}

			var object *extensions.DaemonSet

			if e.Type != watch.Error {
				var ok bool
				object, ok = e.Object.(*extensions.DaemonSet)
				if !ok {
					return true, fmt.Errorf("expected %s to be a *extensions.DaemonSet, got %T", d.ResourceName, e.Object)
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
				err := fmt.Errorf("DaemonSet error: %v", e.Object)
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

// runPodsInformer watch for DaemonSet Pods events
func (d *DaemonSetTracker) runPodsInformer() {
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

func (d *DaemonSetTracker) runPodTracker(podName string) error {
	errorChan := make(chan error, 0)
	doneChan := make(chan struct{}, 0)

	pod := NewPodTracker(d.Context, podName, d.Namespace, d.Kube)
	if !d.LogsFromTime.IsZero() {
		pod.LogsFromTime = d.LogsFromTime
	}
	d.TrackedPods = append(d.TrackedPods, podName)

	go func() {
		if debug() {
			fmt.Printf("Starting DaemonSet's `%s` Pod `%s` tracker. pod state: %v\n", d.ResourceName, pod.ResourceName, pod.State)
		}

		err := pod.Track()
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- struct{}{}
		}

		if debug() {
			fmt.Printf("Done DaemonSet's `%s` Pod `%s` tracker\n", d.ResourceName, pod.ResourceName)
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

func (d *DaemonSetTracker) handleDaemonSetStatus(object *extensions.DaemonSet) (ready bool, err error) {
	if debug() {
		fmt.Printf("%s\n", getDaemonSetStatus(object))
	}

	msg := ""
	msg, ready, err = DaemonSetRolloutStatus(object)

	if debug() {
		evList, err := utils.ListEventsForObject(d.Kube, object)
		if err != nil {
			return false, err
		}
		utils.DescribeEvents(evList)
	}

	if debug() {
		if err == nil && ready {
			fmt.Printf("DaemonSet READY. %s\n", msg)
		} else {
			fmt.Printf("DaemonSet NOT READY. %s\n", msg)
		}
	}

	return ready, err
}

// runEventsInformer watch for DaemonSet events
func (d *DaemonSetTracker) runEventsInformer() {
	if d.lastObject == nil {
		return
	}

	eventInformer := NewEventInformer(d.Tracker, d.lastObject)
	eventInformer.WithChannels(d.EventMsg, d.resourceFailed, d.errors)
	eventInformer.Run()

	return
}
