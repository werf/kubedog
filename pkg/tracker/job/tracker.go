package job

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/werf/kubedog/pkg/utils"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"

	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/tracker/debug"
	"github.com/werf/kubedog/pkg/tracker/event"
	"github.com/werf/kubedog/pkg/tracker/pod"
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

	objectAdded    chan *batchv1.Job
	objectModified chan *batchv1.Job
	objectDeleted  chan *batchv1.Job
	objectFailed   chan string
	errors         chan error

	podAddedRelay chan *corev1.Pod
	// Relay events from subordinate pods to the main Track goroutine.
	// Map data by pod name.
	podStatusesRelay        chan map[string]pod.PodStatus
	podContainerErrorsRelay chan map[string]pod.ContainerErrorReport
	donePodsRelay           chan map[string]pod.PodStatus
}

func NewTracker(name, namespace string, kube kubernetes.Interface, opts tracker.Options) *Tracker {
	return &Tracker{
		Tracker: tracker.Tracker{
			Kube:             kube,
			Namespace:        namespace,
			FullResourceName: fmt.Sprintf("job/%s", name),
			ResourceName:     name,
			LogsFromTime:     opts.LogsFromTime,
		},

		Added:     make(chan JobStatus, 1),
		Succeeded: make(chan JobStatus, 0),
		Failed:    make(chan JobStatus, 0),
		Status:    make(chan JobStatus, 100),

		EventMsg:    make(chan string, 1),
		AddedPod:    make(chan PodAddedReport, 10),
		PodLogChunk: make(chan *pod.PodLogChunk, 1000),
		PodError:    make(chan PodErrorReport, 0),

		podStatuses: make(map[string]pod.PodStatus),

		State: tracker.Initial,

		objectAdded:    make(chan *batchv1.Job, 0),
		objectModified: make(chan *batchv1.Job, 0),
		objectDeleted:  make(chan *batchv1.Job, 0),
		objectFailed:   make(chan string, 1),
		errors:         make(chan error, 0),

		podAddedRelay:           make(chan *corev1.Pod, 0),
		podStatusesRelay:        make(chan map[string]pod.PodStatus, 10),
		podContainerErrorsRelay: make(chan map[string]pod.ContainerErrorReport, 10),
		donePodsRelay:           make(chan map[string]pod.PodStatus, 10),
	}
}

func (job *Tracker) Track(ctx context.Context) error {
	var err error

	err = job.runInformer(ctx)
	if err != nil {
		return err
	}

	for {
		select {
		case object := <-job.objectAdded:
			if err := job.handleJobState(ctx, object); err != nil {
				return err
			}

		case object := <-job.objectModified:
			if err := job.handleJobState(ctx, object); err != nil {
				return err
			}

		case reason := <-job.objectFailed:
			job.State = tracker.ResourceFailed
			job.failedReason = reason

			var status JobStatus
			if job.lastObject != nil {
				job.StatusGeneration++
				status = NewJobStatus(job.lastObject, job.StatusGeneration, (job.State == tracker.ResourceFailed), job.failedReason, job.podStatuses, job.TrackedPodsNames)
			} else {
				status = JobStatus{IsFailed: true, FailedReason: reason}
			}
			job.Failed <- status

		case <-job.objectDeleted:
			job.State = tracker.ResourceDeleted
			job.lastObject = nil
			job.TrackedPodsNames = nil
			job.Status <- JobStatus{}

		case pod := <-job.podAddedRelay:
			if job.lastObject != nil {
				job.StatusGeneration++
				status := NewJobStatus(job.lastObject, job.StatusGeneration, (job.State == tracker.ResourceFailed), job.failedReason, job.podStatuses, job.TrackedPodsNames)
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
				if err := job.handleJobState(ctx, job.lastObject); err != nil {
					return err
				}
			}

		case podStatuses := <-job.podStatusesRelay:
			for podName, podStatus := range podStatuses {
				job.podStatuses[podName] = podStatus
			}
			if job.lastObject != nil {
				if err := job.handleJobState(ctx, job.lastObject); err != nil {
					return err
				}
			}

		case podContainerErrors := <-job.podContainerErrorsRelay:
			for podName, containerError := range podContainerErrors {
				job.podStatuses[podName] = containerError.PodStatus
			}
			if job.lastObject != nil {
				job.StatusGeneration++
				status := NewJobStatus(job.lastObject, job.StatusGeneration, (job.State == tracker.ResourceFailed), job.failedReason, job.podStatuses, job.TrackedPodsNames)

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
			if ctx.Err() == context.Canceled {
				return nil
			}
			return ctx.Err()
		case err := <-job.errors:
			return err
		}
	}
}

func (job *Tracker) runInformer(ctx context.Context) error {
	tweakListOptions := func(options metav1.ListOptions) metav1.ListOptions {
		options.FieldSelector = fields.OneTermEqualSelector("metadata.name", job.ResourceName).String()
		return options
	}
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return job.Kube.BatchV1().Jobs(job.Namespace).List(ctx, tweakListOptions(options))
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return job.Kube.BatchV1().Jobs(job.Namespace).Watch(ctx, tweakListOptions(options))
		},
	}

	go func() {
		_, err := watchtools.UntilWithSync(ctx, lw, &batchv1.Job{}, nil, func(e watch.Event) (bool, error) {
			if debug.Debug() {
				fmt.Printf("Job `%s` informer event: %#v\n", job.ResourceName, e.Type)
			}

			var object *batchv1.Job

			if e.Type != watch.Error {
				var ok bool
				object, ok = e.Object.(*batchv1.Job)
				if !ok {
					return true, fmt.Errorf("expected %s to be a *batchv1.Job, got %T", job.ResourceName, e.Object)
				}
			}

			if e.Type == watch.Added {
				job.objectAdded <- object
			} else if e.Type == watch.Modified {
				job.objectModified <- object
			} else if e.Type == watch.Deleted {
				job.objectDeleted <- object
			}

			return false, nil
		})

		if err != tracker.AdaptInformerError(err) {
			job.errors <- fmt.Errorf("job informer error: %s", err)
		}

		if debug.Debug() {
			fmt.Printf("Job `%s` informer done\n", job.ResourceName)
		}
	}()

	return nil
}

func (job *Tracker) handleJobState(ctx context.Context, object *batchv1.Job) error {
	job.lastObject = object
	job.StatusGeneration++

	status := NewJobStatus(object, job.StatusGeneration, job.State == tracker.ResourceFailed, job.failedReason, job.podStatuses, job.TrackedPodsNames)

	switch job.State {
	case tracker.Initial:
		job.runPodsInformer(ctx, object)

		if os.Getenv("KUBEDOG_DISABLE_EVENTS") != "1" {
			job.runEventsInformer(ctx, object)
		}

		if status.IsFailed {
			job.State = tracker.ResourceFailed
			job.Failed <- status
		} else if status.IsSucceeded {
			job.State = tracker.ResourceSucceeded
			job.Succeeded <- status
		} else {
			job.State = tracker.ResourceAdded
			job.Added <- status
		}
	case tracker.ResourceAdded, tracker.ResourceFailed:
		if status.IsFailed {
			job.State = tracker.ResourceFailed
			job.Failed <- status
		} else if status.IsSucceeded {
			job.State = tracker.ResourceSucceeded
			job.Succeeded <- status
		} else {
			job.Status <- status
		}
	case tracker.ResourceSucceeded:
		job.Status <- status
	case tracker.ResourceDeleted:
		if status.IsFailed {
			job.State = tracker.ResourceFailed
			job.Failed <- status
		} else if status.IsSucceeded {
			job.State = tracker.ResourceSucceeded
			job.Succeeded <- status
		} else {
			job.State = tracker.ResourceAdded
			job.Added <- status
		}
	}

	return nil
}

func (job *Tracker) runPodsInformer(ctx context.Context, object *batchv1.Job) {
	podsInformer := pod.NewPodsInformer(&job.Tracker, utils.ControllerAccessor(object))
	podsInformer.WithChannels(job.podAddedRelay, job.errors)
	podsInformer.Run(ctx)
}

func (job *Tracker) runPodTracker(_ctx context.Context, podName string) error {
	errorChan := make(chan error, 0)
	doneChan := make(chan struct{}, 0)

	newCtx, cancelPodCtx := context.WithCancel(_ctx)
	podTracker := pod.NewTracker(podName, job.Namespace, job.Kube)
	if !job.LogsFromTime.IsZero() {
		podTracker.LogsFromTime = job.LogsFromTime
	}
	job.TrackedPodsNames = append(job.TrackedPodsNames, podName)

	go func() {
		if debug.Debug() {
			fmt.Printf("Starting Job's `%s` Pod `%s` tracker\n", job.ResourceName, podTracker.ResourceName)
		}

		if err := podTracker.Start(newCtx); err != nil {
			errorChan <- err
		} else {
			doneChan <- struct{}{}
		}

		if debug.Debug() {
			fmt.Printf("Done Job's `%s` Pod `%s` tracker done\n", job.ResourceName, podTracker.ResourceName)
		}
	}()

	go func() {
		for {
			select {
			case status := <-podTracker.Added:
				job.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}
			case status := <-podTracker.Succeeded:
				job.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}
				cancelPodCtx()
			case status := <-podTracker.Deleted:
				job.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}
				cancelPodCtx()
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
func (job *Tracker) runEventsInformer(ctx context.Context, object *batchv1.Job) {
	eventInformer := event.NewEventInformer(&job.Tracker, object)
	eventInformer.WithChannels(job.EventMsg, job.objectFailed, job.errors)
	eventInformer.Run(ctx)
}
