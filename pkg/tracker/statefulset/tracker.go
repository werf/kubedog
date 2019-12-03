package statefulset

import (
	"context"
	"fmt"

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

type PodAddedReport struct {
	ReplicaSetPod     replicaset.ReplicaSetPod
	StatefulSetStatus StatefulSetStatus
}

type PodErrorReport struct {
	ReplicaSetPodError replicaset.ReplicaSetPodError
	StatefulSetStatus  StatefulSetStatus
}

type Tracker struct {
	tracker.Tracker

	State      tracker.TrackerState
	Conditions []string

	lastObject   *appsv1.StatefulSet
	failedReason string
	podStatuses  map[string]pod.PodStatus
	podRevisions map[string]string

	TrackedPodsNames []string

	Added  chan StatefulSetStatus
	Ready  chan StatefulSetStatus
	Failed chan StatefulSetStatus
	Status chan StatefulSetStatus

	EventMsg    chan string
	AddedPod    chan PodAddedReport
	PodLogChunk chan *replicaset.ReplicaSetPodLogChunk
	PodError    chan PodErrorReport

	resourceAdded    chan *appsv1.StatefulSet
	resourceModified chan *appsv1.StatefulSet
	resourceDeleted  chan *appsv1.StatefulSet
	resourceFailed   chan string
	errors           chan error

	podAddedRelay           chan *corev1.Pod
	podStatusesRelay        chan map[string]pod.PodStatus
	podLogChunksRelay       chan map[string]*pod.ContainerLogChunk
	podContainerErrorsRelay chan map[string]pod.ContainerErrorReport
	donePodsRelay           chan map[string]pod.PodStatus
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
			LogsFromTime:     opts.LogsFromTime,
		},

		Added:  make(chan StatefulSetStatus, 1),
		Ready:  make(chan StatefulSetStatus, 0),
		Failed: make(chan StatefulSetStatus, 0),
		Status: make(chan StatefulSetStatus, 100),

		EventMsg:    make(chan string, 1),
		AddedPod:    make(chan PodAddedReport, 10),
		PodLogChunk: make(chan *replicaset.ReplicaSetPodLogChunk, 1000),
		PodError:    make(chan PodErrorReport, 0),

		podStatuses:  make(map[string]pod.PodStatus),
		podRevisions: make(map[string]string),

		resourceAdded:    make(chan *appsv1.StatefulSet, 1),
		resourceModified: make(chan *appsv1.StatefulSet, 1),
		resourceDeleted:  make(chan *appsv1.StatefulSet, 1),
		resourceFailed:   make(chan string, 1),
		errors:           make(chan error, 0),

		podAddedRelay:           make(chan *corev1.Pod, 1),
		podStatusesRelay:        make(chan map[string]pod.PodStatus, 10),
		podLogChunksRelay:       make(chan map[string]*pod.ContainerLogChunk, 10),
		podContainerErrorsRelay: make(chan map[string]pod.ContainerErrorReport, 10),
		donePodsRelay:           make(chan map[string]pod.PodStatus, 10),
	}
}

// Track starts tracking of StatefulSet rollout process.
// watch only for one StatefulSet resource with name d.ResourceName within the namespace with name d.Namespace
// Watcher can wait for namespace creation and then for StatefulSet creation
// watcher receives added event if StatefulSet is started
// watch is infinite by default
// there is option StopOnAvailable — if true, watcher stops after StatefulSet has available status
// you can define custom stop triggers using custom implementation of ControllerFeed.
func (d *Tracker) Track() (err error) {
	d.runStatefulSetInformer()

	for {
		select {
		case object := <-d.resourceAdded:
			if err := d.handleStatefulSetState(object); err != nil {
				return err
			}

		case object := <-d.resourceModified:
			if err := d.handleStatefulSetState(object); err != nil {
				return err
			}

		case <-d.resourceDeleted:
			d.State = tracker.ResourceDeleted
			d.lastObject = nil
			d.TrackedPodsNames = nil
			d.podStatuses = make(map[string]pod.PodStatus)
			d.podRevisions = make(map[string]string)
			d.Status <- StatefulSetStatus{}

		case reason := <-d.resourceFailed:
			d.State = tracker.ResourceFailed
			d.failedReason = reason

			var status StatefulSetStatus
			if d.lastObject != nil {
				d.StatusGeneration++
				status = NewStatefulSetStatus(d.lastObject, d.StatusGeneration, (d.State == tracker.ResourceFailed), d.failedReason, d.podStatuses, d.getNewPodsNames())
			} else {
				status = StatefulSetStatus{IsFailed: true, FailedReason: reason}
			}
			d.Failed <- status

		case pod := <-d.podAddedRelay:
			d.podRevisions[pod.Name] = pod.Labels["controller-revision-hash"]

			if d.lastObject != nil {
				d.StatusGeneration++
				status := NewStatefulSetStatus(d.lastObject, d.StatusGeneration, (d.State == tracker.ResourceFailed), d.failedReason, d.podStatuses, d.getNewPodsNames())

				d.AddedPod <- PodAddedReport{
					ReplicaSetPod: replicaset.ReplicaSetPod{
						Name:       pod.Name,
						ReplicaSet: replicaset.ReplicaSet{},
					},
					StatefulSetStatus: status,
				}
			}

			if err := d.runPodTracker(pod.Name); err != nil {
				return err
			}

		case donePods := <-d.donePodsRelay:
			trackedPodsNames := make([]string, 0)

		trackedPodsIteration:
			for _, name := range d.TrackedPodsNames {
				for donePodName, status := range donePods {
					if name == donePodName {
						// This Pod is no more tracked,
						// but we need to update final
						// Pod's status
						if _, hasKey := d.podStatuses[name]; hasKey {
							d.podStatuses[name] = status
						}
						continue trackedPodsIteration
					}
				}

				trackedPodsNames = append(trackedPodsNames, name)
			}
			d.TrackedPodsNames = trackedPodsNames

			if d.lastObject != nil {
				if err := d.handleStatefulSetState(d.lastObject); err != nil {
					return err
				}
			}

		case podStatuses := <-d.podStatusesRelay:
			for podName, podStatus := range podStatuses {
				d.podStatuses[podName] = podStatus
			}
			if d.lastObject != nil {
				if err := d.handleStatefulSetState(d.lastObject); err != nil {
					return err
				}
			}

		case podLogChunks := <-d.podLogChunksRelay:
			for podName, chunk := range podLogChunks {
				d.PodLogChunk <- &replicaset.ReplicaSetPodLogChunk{
					PodLogChunk: &pod.PodLogChunk{
						ContainerLogChunk: chunk,
						PodName:           podName,
					},
					ReplicaSet: replicaset.ReplicaSet{},
				}
			}

		case podContainerErrors := <-d.podContainerErrorsRelay:
			for podName, containerError := range podContainerErrors {
				d.podStatuses[podName] = containerError.PodStatus
			}
			if d.lastObject != nil {
				d.StatusGeneration++
				status := NewStatefulSetStatus(d.lastObject, d.StatusGeneration, (d.State == tracker.ResourceFailed), d.failedReason, d.podStatuses, d.getNewPodsNames())

				for podName, containerError := range podContainerErrors {
					d.PodError <- PodErrorReport{
						ReplicaSetPodError: replicaset.ReplicaSetPodError{
							PodError: pod.PodError{
								ContainerError: containerError.ContainerError,
								PodName:        podName,
							},
							ReplicaSet: replicaset.ReplicaSet{},
						},
						StatefulSetStatus: status,
					}
				}

			}

		case <-d.Context.Done():
			if d.Context.Err() == context.Canceled {
				return nil
			}
			return d.Context.Err()
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

		if err := tracker.AdaptInformerError(err); err != nil {
			d.errors <- err
		}

		if debug.Debug() {
			fmt.Printf("      sts/%s informer DONE\n", d.ResourceName)
		}
	}()

	return
}

// runPodsInformer watch for StatefulSet Pods events
func (d *Tracker) runPodsInformer(object *appsv1.StatefulSet) {
	podsInformer := pod.NewPodsInformer(&d.Tracker, utils.ControllerAccessor(object))
	podsInformer.WithChannels(d.podAddedRelay, d.errors)
	podsInformer.Run()
}

func (d *Tracker) runPodTracker(podName string) error {
	errorChan := make(chan error, 0)
	doneChan := make(chan struct{}, 0)

	ctx, cancelPodCtx := context.WithCancel(d.Context)
	podTracker := pod.NewTracker(ctx, podName, d.Namespace, d.Kube)
	if !d.LogsFromTime.IsZero() {
		podTracker.LogsFromTime = d.LogsFromTime
	}
	d.TrackedPodsNames = append(d.TrackedPodsNames, podName)

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
			case status := <-podTracker.Added:
				d.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}
			case status := <-podTracker.Succeeded:
				d.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}
				cancelPodCtx()
			case report := <-podTracker.Failed:
				d.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: report.PodStatus}
			case status := <-podTracker.Ready:
				d.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}
			case status := <-podTracker.Status:
				d.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}

			case msg := <-podTracker.EventMsg:
				d.EventMsg <- fmt.Sprintf("po/%s %s", podTracker.ResourceName, msg)
			case chunk := <-podTracker.ContainerLogChunk:
				d.podLogChunksRelay <- map[string]*pod.ContainerLogChunk{podTracker.ResourceName: chunk}
			case report := <-podTracker.ContainerError:
				d.podContainerErrorsRelay <- map[string]pod.ContainerErrorReport{podTracker.ResourceName: report}

			case err := <-errorChan:
				d.errors <- err
				return
			case <-doneChan:
				d.donePodsRelay <- map[string]pod.PodStatus{podTracker.ResourceName: podTracker.LastStatus}
				return
			}
		}
	}()

	return nil
}

func (d *Tracker) handleStatefulSetState(object *appsv1.StatefulSet) error {
	d.lastObject = object
	d.StatusGeneration++

	status := NewStatefulSetStatus(object, d.StatusGeneration, (d.State == tracker.ResourceFailed), d.failedReason, d.podStatuses, d.getNewPodsNames())

	switch d.State {
	case tracker.Initial:
		d.runPodsInformer(object)
		d.runEventsInformer(object)

		if status.IsFailed {
			d.State = tracker.ResourceFailed
			d.Failed <- status
		} else if status.IsReady {
			d.State = tracker.ResourceReady
			d.Ready <- status
		} else {
			d.State = tracker.ResourceAdded
			d.Added <- status
		}
	case tracker.ResourceAdded, tracker.ResourceFailed:
		if status.IsFailed {
			d.State = tracker.ResourceFailed
			d.Failed <- status
		} else if status.IsReady {
			d.State = tracker.ResourceReady
			d.Ready <- status
		} else {
			d.Status <- status
		}
	case tracker.ResourceSucceeded:
		d.Status <- status
	case tracker.ResourceDeleted:
		if status.IsFailed {
			d.State = tracker.ResourceFailed
			d.Failed <- status
		} else if status.IsReady {
			d.State = tracker.ResourceReady
			d.Ready <- status
		} else {
			d.State = tracker.ResourceAdded
			d.Added <- status
		}
	}

	return nil
}

// runEventsInformer watch for StatefulSet events
func (d *Tracker) runEventsInformer(object *appsv1.StatefulSet) {
	eventInformer := event.NewEventInformer(&d.Tracker, d.lastObject)
	eventInformer.WithChannels(d.EventMsg, d.resourceFailed, d.errors)
	eventInformer.Run()
}

func (d *Tracker) getNewPodsNames() []string {
	res := []string{}

	for podName, _ := range d.podStatuses {
		if podRevision, hasKey := d.podRevisions[podName]; hasKey {
			if d.lastObject != nil {
				if d.lastObject.Status.UpdateRevision == podRevision {
					res = append(res, podName)
				}
			}
		}
	}

	return res
}
