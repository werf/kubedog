package job

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/samber/lo"
	batchv1 "k8s.io/api/batch/v1"
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
	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
	"github.com/werf/kubedog/pkg/utils"
)

type FailedReport struct {
	FailedReason string
	JobStatus    JobStatus
}

type PodErrorReport struct {
	PodError  pod.PodError
	JobStatus JobStatus
}

type PodAddedReport struct {
	PodName   string
	JobStatus JobStatus
}

type Tracker struct {
	tracker.Tracker
	LogsFromTime time.Time

	Added     chan JobStatus
	Succeeded chan JobStatus
	Failed    chan JobStatus
	Status    chan JobStatus

	EventMsg    chan string
	AddedPod    chan PodAddedReport
	PodLogChunk chan *pod.PodLogChunk
	PodError    chan PodErrorReport

	State            tracker.TrackerState
	TrackedPodsNames []string

	lastObject   *batchv1.Job
	failedReason string
	podStatuses  map[string]pod.PodStatus

	ignoreLogs                               bool
	ignoreReadinessProbeFailsByContainerName map[string]time.Duration

	objectAdded    chan *batchv1.Job
	objectModified chan *batchv1.Job
	objectDeleted  chan *batchv1.Job
	objectFailed   chan interface{}
	errors         chan error

	podAddedRelay chan *corev1.Pod
	// Relay events from subordinate pods to the main Track goroutine.
	// Map data by pod name.
	podStatusesRelay        chan map[string]pod.PodStatus
	podContainerErrorsRelay chan map[string]pod.ContainerErrorReport
	donePodsRelay           chan map[string]pod.PodStatus
}

func NewTracker(name, namespace string, kube kubernetes.Interface, informerFactory *util.Concurrent[*informer.InformerFactory], opts tracker.Options) *Tracker {
	return &Tracker{
		Tracker: tracker.Tracker{
			Kube:             kube,
			Namespace:        namespace,
			FullResourceName: fmt.Sprintf("job/%s", name),
			ResourceName:     name,
			LogsFromTime:     opts.LogsFromTime,
			InformerFactory:  informerFactory,
		},

		Added:     make(chan JobStatus, 1),
		Succeeded: make(chan JobStatus),
		Failed:    make(chan JobStatus),
		Status:    make(chan JobStatus, 100),

		EventMsg:    make(chan string, 1),
		AddedPod:    make(chan PodAddedReport, 10),
		PodLogChunk: make(chan *pod.PodLogChunk, 1000),
		PodError:    make(chan PodErrorReport),

		podStatuses: make(map[string]pod.PodStatus),

		ignoreLogs:                               opts.IgnoreLogs,
		ignoreReadinessProbeFailsByContainerName: opts.IgnoreReadinessProbeFailsByContainerName,

		State: tracker.Initial,

		objectAdded:    make(chan *batchv1.Job),
		objectModified: make(chan *batchv1.Job),
		objectDeleted:  make(chan *batchv1.Job),
		objectFailed:   make(chan interface{}, 1),
		errors:         make(chan error, 1),

		podAddedRelay:           make(chan *corev1.Pod),
		podStatusesRelay:        make(chan map[string]pod.PodStatus, 10),
		podContainerErrorsRelay: make(chan map[string]pod.ContainerErrorReport, 10),
		donePodsRelay:           make(chan map[string]pod.PodStatus, 10),
	}
}

func (job *Tracker) Track(ctx context.Context) error {
	jobInformerCleanupFn, err := job.runInformer(ctx)
	if err != nil {
		return err
	}
	defer jobInformerCleanupFn()

	for {
		select {
		case object := <-job.objectAdded:
			cleanupFn, err := job.handleJobState(ctx, object)
			if err != nil {
				return err
			}
			defer cleanupFn()
		case object := <-job.objectModified:
			cleanupFn, err := job.handleJobState(ctx, object)
			if err != nil {
				return err
			}
			defer cleanupFn()
		case failure := <-job.objectFailed:
			switch failure := failure.(type) {
			case string:
				job.State = tracker.ResourceFailed
				job.failedReason = failure

				var status JobStatus
				if job.lastObject != nil {
					job.StatusGeneration++
					status = NewJobStatus(job.lastObject, job.StatusGeneration, job.State == tracker.ResourceFailed, job.failedReason, job.podStatuses, job.TrackedPodsNames)
				} else {
					status = JobStatus{IsFailed: true, FailedReason: failure}
				}
				job.Failed <- status
			default:
				panic(fmt.Errorf("unexpected type %T", failure))
			}

		case <-job.objectDeleted:
			job.State = tracker.ResourceDeleted
			job.lastObject = nil
			job.TrackedPodsNames = nil
			job.Status <- JobStatus{}

		case pod := <-job.podAddedRelay:
			if job.lastObject != nil {
				job.StatusGeneration++
				status := NewJobStatus(job.lastObject, job.StatusGeneration, job.State == tracker.ResourceFailed, job.failedReason, job.podStatuses, job.TrackedPodsNames)
				job.AddedPod <- PodAddedReport{
					PodName:   pod.Name,
					JobStatus: status,
				}
			}

			if err := job.runPodTracker(ctx, pod.Name); err != nil {
				return err
			}

		case donePods := <-job.donePodsRelay:
			var trackedPodsNames []string

		trackedPodsIteration:
			for _, name := range job.TrackedPodsNames {
				for donePodName, status := range donePods {
					if name == donePodName {
						// This Pod is no more tracked,
						// but we need to update final
						// Pod's status
						if _, hasKey := job.podStatuses[name]; hasKey {
							job.podStatuses[name] = status
						}
						continue trackedPodsIteration
					}
				}

				trackedPodsNames = append(trackedPodsNames, name)
			}
			job.TrackedPodsNames = trackedPodsNames

			if job.lastObject != nil {
				cleanupFn, err := job.handleJobState(ctx, job.lastObject)
				if err != nil {
					return err
				}
				defer cleanupFn()
			}

		case podStatuses := <-job.podStatusesRelay:
			for podName, podStatus := range podStatuses {
				job.podStatuses[podName] = podStatus
			}
			if job.lastObject != nil {
				cleanupFn, err := job.handleJobState(ctx, job.lastObject)
				if err != nil {
					return err
				}
				defer cleanupFn()
			}

		case podContainerErrors := <-job.podContainerErrorsRelay:
			for podName, containerError := range podContainerErrors {
				job.podStatuses[podName] = containerError.PodStatus
			}
			if job.lastObject != nil {
				job.StatusGeneration++
				status := NewJobStatus(job.lastObject, job.StatusGeneration, job.State == tracker.ResourceFailed, job.failedReason, job.podStatuses, job.TrackedPodsNames)

				for podName, containerError := range podContainerErrors {
					job.PodError <- PodErrorReport{
						PodError: pod.PodError{
							ContainerError: containerError.ContainerError,
							PodName:        podName,
						},
						JobStatus: status,
					}
				}
			}

		case <-ctx.Done():
			if debug.Debug() {
				fmt.Printf("Job `%s` tracker context canceled: %s\n", job.ResourceName, context.Cause(ctx))
			}

			return nil
		case err := <-job.errors:
			return err
		}
	}
}

func (job *Tracker) runInformer(ctx context.Context) (cleanupFn func(), err error) {
	var inform *util.Concurrent[*informer.Informer]
	if err := job.InformerFactory.RWTransactionErr(func(factory *informer.InformerFactory) error {
		inform, err = factory.ForNamespace(schema.GroupVersionResource{
			Group:    "batch",
			Version:  "v1",
			Resource: "jobs",
		}, job.Namespace)
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
					jobObj := &batchv1.Job{}
					lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, jobObj))
					return jobObj.Name == job.ResourceName &&
						jobObj.Namespace == job.Namespace
				},
				Handler: cache.ResourceEventHandlerFuncs{
					AddFunc: func(obj interface{}) {
						jobObj := &batchv1.Job{}
						lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, jobObj))
						job.objectAdded <- jobObj
					},
					UpdateFunc: func(oldObj, newObj interface{}) {
						jobObj := &batchv1.Job{}
						lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(newObj.(*unstructured.Unstructured).Object, jobObj))
						job.objectModified <- jobObj
					},
					DeleteFunc: func(obj interface{}) {
						jobObj := &batchv1.Job{}
						lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, jobObj))
						job.objectDeleted <- jobObj
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

func (job *Tracker) handleJobState(ctx context.Context, object *batchv1.Job) (cleanupFn func(), err error) {
	job.lastObject = object
	job.StatusGeneration++

	status := NewJobStatus(object, job.StatusGeneration, job.State == tracker.ResourceFailed, job.failedReason, job.podStatuses, job.TrackedPodsNames)

	cleanupFn = func() {}

	switch job.State {
	case tracker.Initial:
		podsInformerCleanupFn, err := job.runPodsInformer(ctx, object)
		if err != nil {
			return nil, fmt.Errorf("run pods informer: %w", err)
		}

		eventsInformerCleanupFn := func() {}
		if os.Getenv("KUBEDOG_DISABLE_EVENTS") != "1" {
			eventsInformerCleanupFn, err = job.runEventsInformer(ctx, object)
			if err != nil {
				return nil, fmt.Errorf("run events informer: %w", err)
			}
		}

		cleanupFn = func() {
			podsInformerCleanupFn()
			eventsInformerCleanupFn()
		}

		switch {
		case status.IsSucceeded:
			job.State = tracker.ResourceSucceeded
			job.Succeeded <- status
		case status.IsFailed:
			job.State = tracker.ResourceFailed
			job.Failed <- status
		default:
			job.State = tracker.ResourceAdded
			job.Added <- status
		}
	case tracker.ResourceAdded, tracker.ResourceFailed:
		switch {
		case status.IsSucceeded:
			job.State = tracker.ResourceSucceeded
			job.Succeeded <- status
		case status.IsFailed:
			job.State = tracker.ResourceFailed
			job.Failed <- status
		default:
			job.Status <- status
		}
	case tracker.ResourceSucceeded:
		job.Status <- status
	case tracker.ResourceDeleted:
		switch {
		case status.IsSucceeded:
			job.State = tracker.ResourceSucceeded
			job.Succeeded <- status
		case status.IsFailed:
			job.State = tracker.ResourceFailed
			job.Failed <- status
		default:
			job.State = tracker.ResourceAdded
			job.Added <- status
		}
	}

	return cleanupFn, nil
}

func (job *Tracker) runPodsInformer(ctx context.Context, object *batchv1.Job) (cleanupFn func(), err error) {
	podsInformer := pod.NewPodsInformer(&job.Tracker, utils.ControllerAccessor(object))
	podsInformer.WithChannels(job.podAddedRelay, job.errors)
	return podsInformer.Run(ctx)
}

func (job *Tracker) runPodTracker(_ctx context.Context, podName string) error {
	errorChan := make(chan error, 1)
	doneChan := make(chan struct{})

	newCtx, cancelPodCtx := context.WithCancelCause(_ctx)
	podTracker := pod.NewTracker(podName, job.Namespace, job.Kube, job.InformerFactory, pod.Options{
		IgnoreLogs:                               job.ignoreLogs,
		IgnoreReadinessProbeFailsByContainerName: job.ignoreReadinessProbeFailsByContainerName,
	})
	if !job.LogsFromTime.IsZero() {
		podTracker.LogsFromTime = job.LogsFromTime
	}
	job.TrackedPodsNames = append(job.TrackedPodsNames, podName)

	go func() {
		if err := podTracker.Start(newCtx); err != nil {
			errorChan <- err
		} else {
			doneChan <- struct{}{}
		}
	}()

	go func() {
		for {
			select {
			case status := <-podTracker.Added:
				job.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}
			case status := <-podTracker.Succeeded:
				job.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}
				cancelPodCtx(fmt.Errorf("context canceled: got succeeded event for %q", podTracker.FullResourceName))
			case status := <-podTracker.Deleted:
				job.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}
				cancelPodCtx(fmt.Errorf("context canceled: got deleted event for %q", podTracker.FullResourceName))
			case status := <-podTracker.Ready:
				job.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}
			case report := <-podTracker.Failed:
				job.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: report.PodStatus}
			case status := <-podTracker.Status:
				job.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}

			case msg := <-podTracker.EventMsg:
				job.EventMsg <- fmt.Sprintf("po/%s %s", podTracker.ResourceName, msg)
			case chunk := <-podTracker.ContainerLogChunk:
				podChunk := &pod.PodLogChunk{ContainerLogChunk: chunk, PodName: podTracker.ResourceName}
				job.PodLogChunk <- podChunk
			case report := <-podTracker.ContainerError:
				job.podContainerErrorsRelay <- map[string]pod.ContainerErrorReport{podTracker.ResourceName: report}

			case err := <-errorChan:
				job.errors <- err
				return
			case <-doneChan:
				job.donePodsRelay <- map[string]pod.PodStatus{podTracker.ResourceName: podTracker.LastStatus}
				return
			}
		}
	}()

	return nil
}

// runEventsInformer watch for DaemonSet events
func (job *Tracker) runEventsInformer(ctx context.Context, object *batchv1.Job) (cleanupFn func(), err error) {
	eventInformer := event.NewEventInformer(&job.Tracker, object)
	eventInformer.WithChannels(job.EventMsg, job.objectFailed, job.errors)
	return eventInformer.Run(ctx)
}
