package daemonset

import (
	"context"
	"fmt"
	"os"
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

	"github.com/werf/kubedog/for-werf-helm/pkg/tracker"
	"github.com/werf/kubedog/for-werf-helm/pkg/tracker/debug"
	"github.com/werf/kubedog/for-werf-helm/pkg/tracker/event"
	"github.com/werf/kubedog/for-werf-helm/pkg/tracker/pod"
	"github.com/werf/kubedog/for-werf-helm/pkg/tracker/replicaset"
	"github.com/werf/kubedog/for-werf-helm/pkg/utils"
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

	ignoreReadinessProbeFailsByContainerName map[string]time.Duration

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

func NewTracker(name, namespace string, kube kubernetes.Interface, opts tracker.Options) *Tracker {
	return &Tracker{
		Tracker: tracker.Tracker{
			Kube:             kube,
			Namespace:        namespace,
			FullResourceName: fmt.Sprintf("ds/%s", name),
			ResourceName:     name,
			LogsFromTime:     opts.LogsFromTime,
		},

		podStatuses:    make(map[string]pod.PodStatus),
		podGenerations: make(map[string]string),

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
		errors:           make(chan error),

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
	d.runDaemonSetInformer(ctx)

	for {
		select {
		case object := <-d.resourceAdded:
			if err := d.handleDaemonSetState(ctx, object); err != nil {
				return err
			}

		case object := <-d.resourceModified:
			if err := d.handleDaemonSetState(ctx, object); err != nil {
				return err
			}

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
				if err := d.handleDaemonSetState(ctx, d.lastObject); err != nil {
					return err
				}
			}

		case podStatuses := <-d.podStatusesRelay:
			for podName, podStatus := range podStatuses {
				d.podStatuses[podName] = podStatus
			}
			if d.lastObject != nil {
				if err := d.handleDaemonSetState(ctx, d.lastObject); err != nil {
					return err
				}
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
			if ctx.Err() == context.Canceled {
				return nil
			}
			return ctx.Err()
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
func (d *Tracker) runDaemonSetInformer(ctx context.Context) {
	client := d.Kube

	tweakListOptions := func(options metav1.ListOptions) metav1.ListOptions {
		options.FieldSelector = fields.OneTermEqualSelector("metadata.name", d.ResourceName).String()
		return options
	}
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return client.AppsV1().DaemonSets(d.Namespace).List(ctx, tweakListOptions(options))
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return client.AppsV1().DaemonSets(d.Namespace).Watch(ctx, tweakListOptions(options))
		},
	}

	go func() {
		_, err := watchtools.UntilWithSync(ctx, lw, &appsv1.DaemonSet{}, nil, func(e watch.Event) (bool, error) {
			if debug.Debug() {
				fmt.Printf("    Daemonset/%s event: %#v\n", d.ResourceName, e.Type)
			}

			var object *appsv1.DaemonSet

			if e.Type != watch.Error {
				var ok bool
				object, ok = e.Object.(*appsv1.DaemonSet)
				if !ok {
					return true, fmt.Errorf("expected %s to be a *appsv1.DaemonSet, got %T", d.ResourceName, e.Object)
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
				// d.errors <- err
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
}

// runPodsInformer watch for DaemonSet Pods events
func (d *Tracker) runPodsInformer(ctx context.Context, object *appsv1.DaemonSet) {
	podsInformer := pod.NewPodsInformer(&d.Tracker, utils.ControllerAccessor(object))
	podsInformer.WithChannels(d.podAddedRelay, d.errors)
	podsInformer.Run(ctx)
}

func (d *Tracker) runPodTracker(ctx context.Context, podName string) error {
	errorChan := make(chan error)
	doneChan := make(chan struct{})

	newCtx, cancelPodCtx := context.WithCancel(ctx)
	podTracker := pod.NewTracker(podName, d.Namespace, d.Kube, pod.Options{
		IgnoreReadinessProbeFailsByContainerName: d.ignoreReadinessProbeFailsByContainerName,
	})
	if !d.LogsFromTime.IsZero() {
		podTracker.LogsFromTime = d.LogsFromTime
	}
	d.TrackedPodsNames = append(d.TrackedPodsNames, podName)

	go func() {
		if debug.Debug() {
			fmt.Printf("Starting DaemonSet's `%s` Pod `%s` tracker. pod state: %v\n", d.ResourceName, podTracker.ResourceName, podTracker.State)
		}

		err := podTracker.Start(newCtx)
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- struct{}{}
		}

		if debug.Debug() {
			fmt.Printf("Done DaemonSet's `%s` Pod `%s` tracker\n", d.ResourceName, podTracker.ResourceName)
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
			case status := <-podTracker.Deleted:
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

func (d *Tracker) handleDaemonSetState(ctx context.Context, object *appsv1.DaemonSet) error {
	d.lastObject = object
	d.StatusGeneration++

	status := NewDaemonSetStatus(object, d.StatusGeneration, d.State == tracker.ResourceFailed, d.failedReason, d.podStatuses, d.getNewPodsNames())

	switch d.State {
	case tracker.Initial:
		d.runPodsInformer(ctx, object)

		if os.Getenv("KUBEDOG_DISABLE_EVENTS") != "1" {
			d.runEventsInformer(ctx, object)
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

	return nil
}

// runEventsInformer watch for DaemonSet events
func (d *Tracker) runEventsInformer(ctx context.Context, object *appsv1.DaemonSet) {
	eventInformer := event.NewEventInformer(&d.Tracker, object)
	eventInformer.WithChannels(d.EventMsg, d.resourceFailed, d.errors)
	eventInformer.Run(ctx)
}
