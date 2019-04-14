package job

import (
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
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
	"github.com/flant/kubedog/pkg/utils"
)

type JobStatus struct {
	batchv1.JobStatus
	Pods map[string]pod.PodStatus
}

func NewJobStatus(kubeStatus batchv1.JobStatus, podsStatuses map[string]pod.PodStatus) JobStatus {
	res := JobStatus{
		JobStatus: kubeStatus,
		Pods:      make(map[string]pod.PodStatus),
	}
	for k, v := range podsStatuses {
		res.Pods[k] = v
	}
	return res
}

type Tracker struct {
	tracker.Tracker

	Added        chan struct{}
	Succeeded    chan struct{}
	Failed       chan string
	EventMsg     chan string
	AddedPod     chan string
	PodLogChunk  chan *pod.PodLogChunk
	PodError     chan pod.PodError
	StatusReport chan JobStatus

	State          tracker.TrackerState
	TrackedPods    []string
	FinalJobStatus batchv1.JobStatus

	lastObject  *batchv1.Job
	podStatuses map[string]pod.PodStatus

	objectAdded    chan *batchv1.Job
	objectModified chan *batchv1.Job
	objectDeleted  chan *batchv1.Job
	objectFailed   chan string
	podDone        chan string
	errors         chan error
}

func NewTracker(ctx context.Context, name, namespace string, kube kubernetes.Interface) *Tracker {
	return &Tracker{
		Tracker: tracker.Tracker{
			Kube:             kube,
			Namespace:        namespace,
			FullResourceName: fmt.Sprintf("job/%s", name),
			ResourceName:     name,
			Context:          ctx,
		},

		Added:        make(chan struct{}, 0),
		Succeeded:    make(chan struct{}, 0),
		Failed:       make(chan string, 0),
		EventMsg:     make(chan string, 1),
		AddedPod:     make(chan string, 10),
		PodLogChunk:  make(chan *pod.PodLogChunk, 1000),
		PodError:     make(chan pod.PodError, 0),
		StatusReport: make(chan JobStatus, 100),

		podStatuses: make(map[string]pod.PodStatus),

		State:       tracker.Initial,
		TrackedPods: make([]string, 0),

		objectAdded:    make(chan *batchv1.Job, 0),
		objectModified: make(chan *batchv1.Job, 0),
		objectDeleted:  make(chan *batchv1.Job, 0),
		objectFailed:   make(chan string, 1),
		podDone:        make(chan string, 10),
		errors:         make(chan error, 0),
	}
}

func (job *Tracker) Track() error {
	var err error

	err = job.runInformer()
	if err != nil {
		return err
	}

	for {
		select {
		case object := <-job.objectAdded:
			job.lastObject = object
			job.StatusReport <- NewJobStatus(job.lastObject.Status, job.podStatuses)

			job.runEventsInformer()

			switch job.State {
			case tracker.Initial:
				job.State = tracker.ResourceAdded
				job.Added <- struct{}{}

				err = job.runPodsTrackers(object)
				if err != nil {
					return err
				}
			}

			done, err := job.handleJobState(object)
			if err != nil {
				return err
			}
			if done {
				return nil
			}

		case object := <-job.objectModified:
			job.lastObject = object
			job.StatusReport <- NewJobStatus(job.lastObject.Status, job.podStatuses)

			done, err := job.handleJobState(object)
			if err != nil {
				return err
			}
			if done {
				return nil
			}

		case reason := <-job.objectFailed:
			job.lastObject = nil
			job.StatusReport <- JobStatus{}

			job.State = "Failed"
			job.Failed <- reason

		case <-job.objectDeleted:
			if debug.Debug() {
				fmt.Printf("Job `%s` resource gone: stop watching\n", job.ResourceName)
			}
			return nil

		case podName := <-job.podDone:
			trackedPods := make([]string, 0)
			for _, name := range job.TrackedPods {
				if name != podName {
					trackedPods = append(trackedPods, name)
				}
			}
			job.TrackedPods = trackedPods

			done, err := job.handleJobState(job.lastObject)
			if err != nil {
				return err
			}
			if done {
				return nil
			}

		case <-job.Context.Done():
			return tracker.ErrTrackInterrupted

		case err := <-job.errors:
			return err
		}
	}
}

func (job *Tracker) runInformer() error {
	tweakListOptions := func(options metav1.ListOptions) metav1.ListOptions {
		options.FieldSelector = fields.OneTermEqualSelector("metadata.name", job.ResourceName).String()
		return options
	}
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return job.Kube.BatchV1().Jobs(job.Namespace).List(tweakListOptions(options))
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return job.Kube.BatchV1().Jobs(job.Namespace).Watch(tweakListOptions(options))
		},
	}

	go func() {
		_, err := watchtools.UntilWithSync(job.Context, lw, &batchv1.Job{}, nil, func(e watch.Event) (bool, error) {
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

		if err != nil {
			job.errors <- err
		}

		if debug.Debug() {
			fmt.Printf("Job `%s` informer done\n", job.ResourceName)
		}
	}()

	return nil
}

func (job *Tracker) handleJobState(object *batchv1.Job) (done bool, err error) {
	if debug.Debug() {
		evList, err := utils.ListEventsForObject(job.Kube, object)
		if err != nil {
			return false, err
		}
		utils.DescribeEvents(evList)
	}

	if len(job.TrackedPods) == 0 {
		for _, c := range object.Status.Conditions {
			if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
				job.State = tracker.ResourceSucceeded
				job.FinalJobStatus = object.Status
				job.Succeeded <- struct{}{}
				done = true
			} else if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
				job.State = tracker.ResourceFailed
				job.Failed <- c.Reason
				done = true
			}
		}
	}

	return
}

func (job *Tracker) runPodsTrackers(object *batchv1.Job) error {
	selector, err := metav1.LabelSelectorAsSelector(object.Spec.Selector)
	if err != nil {
		return err
	}

	list, err := job.Kube.CoreV1().Pods(job.Namespace).List(metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return err
	}
	for _, item := range list.Items {
		err := job.runPodTracker(item.Name)
		if err != nil {
			return err
		}
	}

	tweakListOptions := func(options metav1.ListOptions) metav1.ListOptions {
		options.LabelSelector = selector.String()
		return options
	}
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return job.Kube.CoreV1().Pods(job.Namespace).List(tweakListOptions(options))
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return job.Kube.CoreV1().Pods(job.Namespace).Watch(tweakListOptions(options))
		},
	}

	go func() {
		_, err = watchtools.UntilWithSync(job.Context, lw, &corev1.Pod{}, nil, func(e watch.Event) (bool, error) {
			if debug.Debug() {
				fmt.Printf("Job `%s` pods informer event: %#v\n", job.ResourceName, e.Type)
			}

			object, ok := e.Object.(*corev1.Pod)
			if !ok {
				return true, fmt.Errorf("expected %s to be a *corev1.Pod, got %T", job.ResourceName, e.Object)
			}

			if e.Type == watch.Added {
				for _, podName := range job.TrackedPods {
					if podName == object.Name {
						// Already under tracking
						return false, nil
					}
				}

				err := job.runPodTracker(object.Name)
				if err != nil {
					return true, err
				}
			}

			return false, nil
		})

		if err != nil {
			job.errors <- err
		}

		if debug.Debug() {
			fmt.Printf("Job `%s` pods informer done\n", job.ResourceName)
		}
	}()

	return nil
}

func (job *Tracker) runPodTracker(podName string) error {
	errorChan := make(chan error, 0)
	doneChan := make(chan struct{}, 0)

	podTracker := pod.NewTracker(job.Context, podName, job.Namespace, job.Kube)
	job.TrackedPods = append(job.TrackedPods, podName)

	job.AddedPod <- podTracker.ResourceName

	go func() {
		if debug.Debug() {
			fmt.Printf("Starting Job's `%s` Pod `%s` tracker\n", job.ResourceName, podTracker.ResourceName)
		}

		err := podTracker.Start()
		if err != nil {
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
			case chunk := <-podTracker.ContainerLogChunk:
				podChunk := &pod.PodLogChunk{ContainerLogChunk: chunk, PodName: podTracker.ResourceName}
				job.PodLogChunk <- podChunk
			case containerError := <-podTracker.ContainerError:
				podError := pod.PodError{ContainerError: containerError, PodName: podTracker.ResourceName}
				job.PodError <- podError
			case msg := <-podTracker.EventMsg:
				job.EventMsg <- fmt.Sprintf("po/%s %s", podTracker.ResourceName, msg)
			case <-podTracker.Added:
			case <-podTracker.Succeeded:
			case <-podTracker.Failed:
			case <-podTracker.Ready:
			case podStatus := <-podTracker.StatusReport:
				job.podStatuses[podTracker.ResourceName] = podStatus
				if job.lastObject != nil {
					job.StatusReport <- NewJobStatus(job.lastObject.Status, job.podStatuses)
				}

			case err := <-errorChan:
				job.errors <- err
				return
			case <-doneChan:
				job.podDone <- podTracker.ResourceName
				return
			}
		}
	}()

	return nil
}

// runEventsInformer watch for DaemonSet events
func (job *Tracker) runEventsInformer() {
	if job.lastObject == nil {
		return
	}

	eventInformer := event.NewEventInformer(&job.Tracker, job.lastObject)
	eventInformer.WithChannels(job.EventMsg, job.objectFailed, job.errors)
	eventInformer.Run()

	return
}
