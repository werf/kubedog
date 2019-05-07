package statefulset

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

	"github.com/flant/kubedog/pkg/tracker"
	"github.com/flant/kubedog/pkg/tracker/debug"
	"github.com/flant/kubedog/pkg/tracker/event"
	"github.com/flant/kubedog/pkg/tracker/pod"
	"github.com/flant/kubedog/pkg/tracker/replicaset"
	"github.com/flant/kubedog/pkg/utils"
)

type StatefulSetStatus struct {
	appsv1.StatefulSetStatus
	Pods map[string]pod.PodStatus
}

type Tracker struct {
	tracker.Tracker
	LogsFromTime time.Time

	State                  string
	Conditions             []string
	FinalStatefulSetStatus appsv1.StatefulSetStatus
	lastObject             *appsv1.StatefulSet
	podStatuses            map[string]pod.PodStatus

	Added        chan bool
	Ready        chan bool
	Failed       chan string
	EventMsg     chan string
	AddedPod     chan replicaset.ReplicaSetPod
	PodLogChunk  chan *replicaset.ReplicaSetPodLogChunk
	PodError     chan replicaset.ReplicaSetPodError
	StatusReport chan StatefulSetStatus

	resourceAdded     chan *appsv1.StatefulSet
	resourceModified  chan *appsv1.StatefulSet
	resourceDeleted   chan *appsv1.StatefulSet
	resourceFailed    chan string
	podAdded          chan *corev1.Pod
	podDone           chan string
	errors            chan error
	podStatusesReport chan map[string]pod.PodStatus

	TrackedPods []string
}

func NewTracker(ctx context.Context, name, namespace string, kube kubernetes.Interface, opts tracker.Options) *Tracker {
	if debug.Debug() {
		fmt.Printf("> statefulset.NewTracker\n")
	}
	return &Tracker{
		Tracker: tracker.Tracker{
			Kube:             kube,
			Namespace:        namespace,
			FullResourceName: fmt.Sprintf("sts/%s", name),
			ResourceName:     name,
			Context:          ctx,
		},

		LogsFromTime: opts.LogsFromTime,

		Added:        make(chan bool, 0),
		Ready:        make(chan bool, 1),
		Failed:       make(chan string, 1),
		EventMsg:     make(chan string, 1),
		AddedPod:     make(chan replicaset.ReplicaSetPod, 10),
		PodLogChunk:  make(chan *replicaset.ReplicaSetPodLogChunk, 1000),
		PodError:     make(chan replicaset.ReplicaSetPodError, 0),
		StatusReport: make(chan StatefulSetStatus, 100),

		podStatuses: make(map[string]pod.PodStatus),
		TrackedPods: make([]string, 0),

		resourceAdded:     make(chan *appsv1.StatefulSet, 1),
		resourceModified:  make(chan *appsv1.StatefulSet, 1),
		resourceDeleted:   make(chan *appsv1.StatefulSet, 1),
		resourceFailed:    make(chan string, 1),
		podAdded:          make(chan *corev1.Pod, 1),
		podDone:           make(chan string, 1),
		errors:            make(chan error, 0),
		podStatusesReport: make(chan map[string]pod.PodStatus),
	}
}

func NewStatefulSetStatus(kubeStatus appsv1.StatefulSetStatus, podsStatuses map[string]pod.PodStatus) StatefulSetStatus {
	res := StatefulSetStatus{
		StatefulSetStatus: kubeStatus,
		Pods:              make(map[string]pod.PodStatus),
	}
	for k, v := range podsStatuses {
		res.Pods[k] = v
	}
	return res
}

// Track starts tracking of StatefulSet rollout process.
// watch only for one StatefulSet resource with name d.ResourceName within the namespace with name d.Namespace
// Watcher can wait for namespace creation and then for StatefulSet creation
// watcher receives added event if StatefulSet is started
// watch is infinite by default
// there is option StopOnAvailable — if true, watcher stops after StatefulSet has available status
// you can define custom stop triggers using custom implementation of ControllerFeed.
func (d *Tracker) Track() (err error) {
	if debug.Debug() {
		fmt.Printf("> Tracker.Track()\n")
	}

	d.runStatefulSetInformer()

	for {
		select {
		case object := <-d.resourceAdded:
			d.lastObject = object
			d.StatusReport <- NewStatefulSetStatus(d.lastObject.Status, d.podStatuses)

			ready := d.handleStatefulSetState(object)
			if debug.Debug() {
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
			d.StatusReport <- NewStatefulSetStatus(d.lastObject.Status, d.podStatuses)

			ready := d.handleStatefulSetState(object)
			if ready {
				d.FinalStatefulSetStatus = object.Status
				d.Ready <- true
			}
		case <-d.resourceDeleted:
			d.lastObject = nil
			d.StatusReport <- StatefulSetStatus{}

			d.State = "Deleted"
			d.Failed <- "resource deleted"
			// TODO: This is not fail on tracker level

		case reason := <-d.resourceFailed:
			d.State = "Failed"
			d.Failed <- reason

		case pod := <-d.podAdded:
			if debug.Debug() {
				fmt.Printf("po/%s added\n", pod.Name)
			}

			rsPod := replicaset.ReplicaSetPod{
				Name:       pod.Name,
				ReplicaSet: replicaset.ReplicaSet{},
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

		case podStatuses := <-d.podStatusesReport:
			for podName, podStatus := range podStatuses {
				d.podStatuses[podName] = podStatus
			}
			if d.lastObject != nil {
				d.StatusReport <- NewStatefulSetStatus(d.lastObject.Status, d.podStatuses)
			}

		case <-d.Context.Done():
			return tracker.ErrTrackInterrupted

		case err := <-d.errors:
			return err
		}
	}

	return err
}

// runStatefulSetInformer watch for StatefulSet events
func (d *Tracker) runStatefulSetInformer() {
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
			if debug.Debug() {
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

		if debug.Debug() {
			fmt.Printf("      sts/%s informer DONE\n", d.ResourceName)
		}
	}()

	return
}

// runPodsInformer watch for StatefulSet Pods events
func (d *Tracker) runPodsInformer() {
	if d.lastObject == nil {
		// This shouldn't happen!
		// TODO add error
		return
	}

	podsInformer := pod.NewPodsInformer(&d.Tracker, utils.ControllerAccessor(d.lastObject))
	podsInformer.WithChannels(d.podAdded, d.errors)
	podsInformer.Run()

	return
}

func (d *Tracker) runPodTracker(podName string) error {
	errorChan := make(chan error, 0)
	doneChan := make(chan struct{}, 0)

	podTracker := pod.NewTracker(d.Context, podName, d.Namespace, d.Kube)
	if !d.LogsFromTime.IsZero() {
		podTracker.LogsFromTime = d.LogsFromTime
	}
	d.TrackedPods = append(d.TrackedPods, podName)

	go func() {
		if debug.Debug() {
			fmt.Printf("Starting StatefulSet's `%s` Pod `%s` tracker. pod state: %v\n", d.ResourceName, podTracker.ResourceName, podTracker.State)
		}

		err := podTracker.Start()
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- struct{}{}
		}

		if debug.Debug() {
			fmt.Printf("Done StatefulSet's `%s` Pod `%s` tracker\n", d.ResourceName, podTracker.ResourceName)
		}
	}()

	go func() {
		for {
			select {
			case chunk := <-podTracker.ContainerLogChunk:
				rsChunk := &replicaset.ReplicaSetPodLogChunk{
					PodLogChunk: &pod.PodLogChunk{
						ContainerLogChunk: chunk,
						PodName:           podTracker.ResourceName,
					},
					ReplicaSet: replicaset.ReplicaSet{},
				}

				d.PodLogChunk <- rsChunk
			case containerError := <-podTracker.ContainerError:
				podError := replicaset.ReplicaSetPodError{
					PodError: pod.PodError{
						ContainerError: containerError,
						PodName:        podTracker.ResourceName,
					},
					ReplicaSet: replicaset.ReplicaSet{},
				}

				d.PodError <- podError
			case msg := <-podTracker.EventMsg:
				d.EventMsg <- fmt.Sprintf("po/%s %s", podTracker.ResourceName, msg)
			case <-podTracker.Added:
			case <-podTracker.Succeeded:
			case <-podTracker.Failed:
			case <-podTracker.Ready:
			case podStatus := <-podTracker.StatusReport:
				d.podStatusesReport <- map[string]pod.PodStatus{podTracker.ResourceName: podStatus}
			case err := <-errorChan:
				d.errors <- err
				return
			case <-doneChan:
				d.podDone <- podTracker.ResourceName
				return
			}
		}
	}()

	return nil
}

func (d *Tracker) handleStatefulSetState(object *appsv1.StatefulSet) bool {
	if debug.Debug() {
		fmt.Printf("%s\n", getStatefulSetStatus(object))
		msg, ready, err := StatefulSetRolloutStatus(object)
		fmt.Printf("StatefulSet kubectl rollout status: ready=%s, msg=%s, err=%v\n", debug.YesNo(ready), msg, err)

		evList, err := utils.ListEventsForObject(d.Kube, object)
		if err != nil {
			fmt.Printf("ListEvents for sts/%s error: %v\n", object.Name, err)
		} else {
			utils.DescribeEvents(evList)
		}
	}

	return StatefulSetComplete(object)
}

// runEventsInformer watch for StatefulSet events
func (d *Tracker) runEventsInformer() {
	if d.lastObject == nil {
		return
	}

	eventInformer := event.NewEventInformer(&d.Tracker, d.lastObject)
	eventInformer.WithChannels(d.EventMsg, d.resourceFailed, d.errors)
	eventInformer.Run()

	return
}
