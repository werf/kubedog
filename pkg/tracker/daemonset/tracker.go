package daemonset

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/tracker/debug"
	"github.com/werf/kubedog/pkg/tracker/event"
	"github.com/werf/kubedog/pkg/tracker/pod"
	"github.com/werf/kubedog/pkg/tracker/replicaset"
	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
	"github.com/werf/kubedog/pkg/utils"
)

type PodAddedReport struct {
	// FIXME !!! DaemonSet is not like Deployment.
	// FIXME !!! DaemonSet is not related to ReplicaSet.
	// FIXME !!! Pods of DaemonSet have owner reference directly to ReplicaSet.
	// FIXME !!! Delete all ReplicaSet-related data.
	Pod             replicaset.ReplicaSetPod
	DaemonSetStatus DaemonSetStatus
}

type PodErrorReport struct {
	PodError        replicaset.ReplicaSetPodError
	DaemonSetStatus DaemonSetStatus
}

type Tracker struct {
	tracker.Tracker

	State            tracker.TrackerState
	TrackedPodsNames []string
	Conditions       []string

	Added  chan DaemonSetStatus
	Ready  chan DaemonSetStatus
	Failed chan DaemonSetStatus
	Status chan DaemonSetStatus

	EventMsg    chan string
	AddedPod    chan PodAddedReport
	PodLogChunk chan *replicaset.ReplicaSetPodLogChunk
	PodError    chan PodErrorReport

	ignoreLogs                               bool
	ignoreReadinessProbeFailsByContainerName map[string]time.Duration
	savingLogsReplicas                       int

	lastObject     *appsv1.DaemonSet
	failedReason   string
	podStatuses    map[string]pod.PodStatus
	podGenerations map[string]string

	resourceAdded    chan *appsv1.DaemonSet
	resourceModified chan *appsv1.DaemonSet
	resourceDeleted  chan *appsv1.DaemonSet
	resourceFailed   chan interface{}
	errors           chan error

	podAddedRelay           chan *corev1.Pod
	podStatusesRelay        chan map[string]pod.PodStatus
	podContainerErrorsRelay chan map[string]pod.ContainerErrorReport
	donePodsRelay           chan map[string]pod.PodStatus
}

func NewTracker(name, namespace string, kube kubernetes.Interface, informerFactory *util.Concurrent[*informer.InformerFactory], opts tracker.Options) *Tracker {
	return &Tracker{
		Tracker: tracker.Tracker{
			Kube:                            kube,
			Namespace:                       namespace,
			FullResourceName:                fmt.Sprintf("ds/%s", name),
			ResourceName:                    name,
			SaveLogsOnlyForNumberOfReplicas: opts.SaveLogsOnlyForNumberOfReplicas,
			LogsFromTime:                    opts.LogsFromTime,
			InformerFactory:                 informerFactory,
		},

		podStatuses:    make(map[string]pod.PodStatus),
		podGenerations: make(map[string]string),

		ignoreLogs:                               opts.IgnoreLogs,
		ignoreReadinessProbeFailsByContainerName: opts.IgnoreReadinessProbeFailsByContainerName,

		Added:  make(chan DaemonSetStatus, 1),
		Ready:  make(chan DaemonSetStatus),
		Failed: make(chan DaemonSetStatus),

		EventMsg:    make(chan string, 1),
		AddedPod:    make(chan PodAddedReport, 10),
		PodLogChunk: make(chan *replicaset.ReplicaSetPodLogChunk, 1000),
		PodError:    make(chan PodErrorReport),
		Status:      make(chan DaemonSetStatus, 100),

		resourceAdded:    make(chan *appsv1.DaemonSet, 1),
		resourceModified: make(chan *appsv1.DaemonSet, 1),
		resourceDeleted:  make(chan *appsv1.DaemonSet, 1),
		resourceFailed:   make(chan interface{}, 1),
		errors:           make(chan error, 1),

		podAddedRelay:           make(chan *corev1.Pod, 1),
		podStatusesRelay:        make(chan map[string]pod.PodStatus, 10),
		podContainerErrorsRelay: make(chan map[string]pod.ContainerErrorReport, 10),
		donePodsRelay:           make(chan map[string]pod.PodStatus, 1),
	}
}

// Track starts tracking of DaemonSet rollout process.
// watch only for one DaemonSet resource with name d.ResourceName within the namespace with name d.Namespace
// Watcher can wait for namespace creation and then for DaemonSet creation
// watcher receives added event if DaemonSet is started
// watch is infinite by default
// there is option StopOnAvailable — if true, watcher stops after DaemonSet has available status
// you can define custom stop triggers using custom implementation of ControllerFeed.
func (d *Tracker) Track(ctx context.Context) error {
	daemonsetInformerCleanupFn, err := d.runDaemonSetInformer(ctx)
	if err != nil {
		return err
	}
	defer daemonsetInformerCleanupFn()

	for {
		select {
		case object := <-d.resourceAdded:
			cleanupFn, err := d.handleDaemonSetState(ctx, object)
			if err != nil {
				return err
			}
			defer cleanupFn()
		case object := <-d.resourceModified:
			cleanupFn, err := d.handleDaemonSetState(ctx, object)
			if err != nil {
				return err
			}
			defer cleanupFn()
		case <-d.resourceDeleted:
			d.State = tracker.ResourceDeleted
			d.lastObject = nil
			d.TrackedPodsNames = nil
			d.podStatuses = make(map[string]pod.PodStatus)
			d.podGenerations = make(map[string]string)
			d.Status <- DaemonSetStatus{}

		case failure := <-d.resourceFailed:
			switch reason := failure.(type) {
			case string:
				d.State = tracker.ResourceFailed
				d.failedReason = reason

				var status DaemonSetStatus
				if d.lastObject != nil {
					d.StatusGeneration++
					status = NewDaemonSetStatus(d.lastObject, d.StatusGeneration, d.State == tracker.ResourceFailed, d.failedReason, d.podStatuses, d.getNewPodsNames())
				} else {
					status = DaemonSetStatus{IsFailed: true, FailedReason: reason}
				}
				d.Failed <- status
			default:
				panic(fmt.Errorf("unexpected type %T", reason))
			}

		case pod := <-d.podAddedRelay:
			d.podGenerations[pod.Name] = pod.Labels["pod-template-generation"]

			if d.lastObject != nil {
				d.StatusGeneration++
				status := NewDaemonSetStatus(d.lastObject, d.StatusGeneration, d.State == tracker.ResourceFailed, d.failedReason, d.podStatuses, d.getNewPodsNames())
				d.AddedPod <- PodAddedReport{
					Pod: replicaset.ReplicaSetPod{
						Name:       pod.Name,
						ReplicaSet: replicaset.ReplicaSet{},
					},
					DaemonSetStatus: status,
				}
			}

			err := d.runPodTracker(ctx, pod.Name)
			if err != nil {
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
				cleanupFn, err := d.handleDaemonSetState(ctx, d.lastObject)
				if err != nil {
					return err
				}
				defer cleanupFn()
			}

		case podStatuses := <-d.podStatusesRelay:
			for podName, podStatus := range podStatuses {
				d.podStatuses[podName] = podStatus
			}
			if d.lastObject != nil {
				cleanupFn, err := d.handleDaemonSetState(ctx, d.lastObject)
				if err != nil {
					return err
				}
				defer cleanupFn()
			}

		case podContainerErrors := <-d.podContainerErrorsRelay:
			for podName, containerError := range podContainerErrors {
				d.podStatuses[podName] = containerError.PodStatus
			}
			if d.lastObject != nil {
				d.StatusGeneration++
				status := NewDaemonSetStatus(d.lastObject, d.StatusGeneration, d.State == tracker.ResourceFailed, d.failedReason, d.podStatuses, d.getNewPodsNames())

				for podName, containerError := range podContainerErrors {
					d.PodError <- PodErrorReport{
						PodError: replicaset.ReplicaSetPodError{
							PodError: pod.PodError{
								ContainerError: containerError.ContainerError,
								PodName:        podName,
							},
							ReplicaSet: replicaset.ReplicaSet{},
						},
						DaemonSetStatus: status,
					}
				}
			}

		case <-ctx.Done():
			if debug.Debug() {
				fmt.Printf("DaemonSet `%s` tracker context canceled: %s\n", d.ResourceName, context.Cause(ctx))
			}

			return context.Cause(ctx)
		case err := <-d.errors:
			return err
		}
	}
}

func (d *Tracker) getNewPodsNames() []string {
	res := []string{}

	for podName := range d.podStatuses {
		if podGeneration, hasKey := d.podGenerations[podName]; hasKey {
			if d.lastObject != nil {
				if fmt.Sprintf("%d", d.lastObject.Generation) == podGeneration {
					res = append(res, podName)
				}
			}
		}
	}

	return res
}

// runDaemonSetInformer watch for DaemonSet events
func (d *Tracker) runDaemonSetInformer(ctx context.Context) (cleanupFn func(), err error) {
	var inform *util.Concurrent[*informer.Informer]
	if err := d.InformerFactory.RWTransactionErr(func(factory *informer.InformerFactory) error {
		inform, err = factory.ForNamespace(schema.GroupVersionResource{
			Group:    "apps",
			Version:  "v1",
			Resource: "daemonsets",
		}, d.Namespace)
		if err != nil {
			return fmt.Errorf("get informer from factory: %w", err)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	if err := inform.RWTransactionErr(func(inf *informer.Informer) error {
		handler, err := inf.AddEventHandler(
			cache.FilteringResourceEventHandler{
				FilterFunc: func(obj interface{}) bool {
					if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
						obj = d.Obj
					}

					daemonsetObj := &appsv1.DaemonSet{}
					lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, daemonsetObj))
					return daemonsetObj.Name == d.ResourceName &&
						daemonsetObj.Namespace == d.Namespace
				},
				Handler: cache.ResourceEventHandlerFuncs{
					AddFunc: func(obj interface{}) {
						if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
							obj = d.Obj
						}

						daemonsetObj := &appsv1.DaemonSet{}
						lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, daemonsetObj))
						d.resourceAdded <- daemonsetObj
					},
					UpdateFunc: func(oldObj, newObj interface{}) {
						if d, ok := newObj.(cache.DeletedFinalStateUnknown); ok {
							newObj = d.Obj
						}

						daemonsetObj := &appsv1.DaemonSet{}
						lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(newObj.(*unstructured.Unstructured).Object, daemonsetObj))
						d.resourceModified <- daemonsetObj
					},
					DeleteFunc: func(obj interface{}) {
						if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
							obj = d.Obj
						}

						daemonsetObj := &appsv1.DaemonSet{}
						lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, daemonsetObj))
						d.resourceDeleted <- daemonsetObj
					},
				},
			},
		)
		if err != nil {
			return fmt.Errorf("add event handler: %w", err)
		}

		cleanupFn = func() {
			inf.RemoveEventHandler(handler)
		}

		inf.Run()

		return nil
	}); err != nil {
		return nil, err
	}

	return cleanupFn, nil
}

// runPodsInformer watch for DaemonSet Pods events
func (d *Tracker) runPodsInformer(ctx context.Context, object *appsv1.DaemonSet) (cleanupFn func(), err error) {
	podsInformer := pod.NewPodsInformer(&d.Tracker, utils.ControllerAccessor(object))
	podsInformer.WithChannels(d.podAddedRelay, d.errors)
	return podsInformer.Run(ctx)
}

func (d *Tracker) runPodTracker(ctx context.Context, podName string) error {
	errorChan := make(chan error, 1)
	doneChan := make(chan struct{})

	ignoreLogs := d.ignoreLogs || d.savingLogsReplicas >= d.SaveLogsOnlyForNumberOfReplicas
	if !ignoreLogs {
		d.savingLogsReplicas++
	}

	newCtx, cancelPodCtx := context.WithCancelCause(ctx)
	podTracker := pod.NewTracker(podName, d.Namespace, d.Kube, d.InformerFactory, pod.Options{
		IgnoreLogs:                               ignoreLogs,
		IgnoreReadinessProbeFailsByContainerName: d.ignoreReadinessProbeFailsByContainerName,
	})
	if !d.LogsFromTime.IsZero() {
		podTracker.LogsFromTime = d.LogsFromTime
	}
	d.TrackedPodsNames = append(d.TrackedPodsNames, podName)

	go func() {
		err := podTracker.Start(newCtx)
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- struct{}{}
		}
	}()

	go func() {
		for {
			select {
			case status := <-podTracker.Added:
				d.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}
			case status := <-podTracker.Succeeded:
				d.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}
				cancelPodCtx(fmt.Errorf("context canceled: got succeeded event for %q", podTracker.FullResourceName))
			case status := <-podTracker.Deleted:
				d.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}
				cancelPodCtx(fmt.Errorf("context canceled: got deleted event for %q", podTracker.FullResourceName))
			case report := <-podTracker.Failed:
				d.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: report.PodStatus}
			case status := <-podTracker.Ready:
				d.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}
			case status := <-podTracker.Status:
				d.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}

			case msg := <-podTracker.EventMsg:
				d.EventMsg <- fmt.Sprintf("po/%s %s", podTracker.ResourceName, msg)
			case chunk := <-podTracker.ContainerLogChunk:
				rsChunk := &replicaset.ReplicaSetPodLogChunk{
					PodLogChunk: &pod.PodLogChunk{
						ContainerLogChunk: chunk,
						PodName:           podTracker.ResourceName,
					},
					ReplicaSet: replicaset.ReplicaSet{},
				}

				d.PodLogChunk <- rsChunk
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

func (d *Tracker) handleDaemonSetState(ctx context.Context, object *appsv1.DaemonSet) (cleanupFn func(), err error) {
	d.lastObject = object
	d.StatusGeneration++

	status := NewDaemonSetStatus(object, d.StatusGeneration, d.State == tracker.ResourceFailed, d.failedReason, d.podStatuses, d.getNewPodsNames())

	cleanupFn = func() {}

	switch d.State {
	case tracker.Initial:
		podsInformerCleanupFn, err := d.runPodsInformer(ctx, object)
		if err != nil {
			return nil, fmt.Errorf("run pods informer: %w", err)
		}

		eventsInformerCleanupFn := func() {}
		if os.Getenv("KUBEDOG_DISABLE_EVENTS") != "1" {
			eventsInformerCleanupFn, err = d.runEventsInformer(ctx, object)
			if err != nil {
				return nil, fmt.Errorf("run events informer: %w", err)
			}
		}

		cleanupFn = func() {
			podsInformerCleanupFn()
			eventsInformerCleanupFn()
		}

		switch {
		case status.IsReady:
			d.State = tracker.ResourceReady
			d.Ready <- status
		case status.IsFailed:
			d.State = tracker.ResourceFailed
			d.Failed <- status
		default:
			d.State = tracker.ResourceAdded
			d.Added <- status
		}
	case tracker.ResourceAdded, tracker.ResourceFailed:
		switch {
		case status.IsReady:
			d.State = tracker.ResourceReady
			d.Ready <- status
		case status.IsFailed:
			d.State = tracker.ResourceFailed
			d.Failed <- status
		default:
			d.Status <- status
		}
	case tracker.ResourceSucceeded:
		d.Status <- status
	case tracker.ResourceDeleted:
		switch {
		case status.IsReady:
			d.State = tracker.ResourceReady
			d.Ready <- status
		case status.IsFailed:
			d.State = tracker.ResourceFailed
			d.Failed <- status
		default:
			d.State = tracker.ResourceAdded
			d.Added <- status
		}
	}

	return cleanupFn, nil
}

// runEventsInformer watch for DaemonSet events
func (d *Tracker) runEventsInformer(ctx context.Context, object *appsv1.DaemonSet) (cleanupFn func(), err error) {
	eventInformer := event.NewEventInformer(&d.Tracker, object)
	eventInformer.WithChannels(d.EventMsg, d.resourceFailed, d.errors)
	return eventInformer.Run(ctx)
}
