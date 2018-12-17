package tracker

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

	"github.com/flant/kubedog/pkg/utils"
)

type JobFeed interface {
	Added() error
	Succeeded() error
	Failed(reason string) error
	EventMsg(msg string) error
	AddedPod(podName string) error
	PodLogChunk(*PodLogChunk) error
	PodError(PodError) error
}

func TrackJob(name, namespace string, kube kubernetes.Interface, feed JobFeed, opts Options) error {
	errorChan := make(chan error, 0)
	doneChan := make(chan struct{}, 0)

	parentContext := opts.ParentContext
	if parentContext == nil {
		parentContext = context.Background()
	}
	ctx, cancel := watchtools.ContextWithOptionalTimeout(parentContext, opts.Timeout)
	defer cancel()

	job := NewJobTracker(ctx, name, namespace, kube)

	go func() {
		err := job.Track()
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- struct{}{}
		}
	}()

	for {
		select {
		case <-job.Added:
			if debug() {
				fmt.Printf("Job `%s` added\n", job.ResourceName)
			}

			err := feed.Added()
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case <-job.Succeeded:
			if debug() {
				fmt.Printf("Job `%s` succeeded: pods failed: %d, pods succeeded: %d\n", job.ResourceName, job.FinalJobStatus.Failed, job.FinalJobStatus.Succeeded)
			}

			err := feed.Succeeded()
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case reason := <-job.Failed:
			if debug() {
				fmt.Printf("Job `%s` failed: %s\n", job.ResourceName, reason)
			}

			err := feed.Failed(reason)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case msg := <-job.EventMsg:
			if debug() {
				fmt.Printf("Job `%s` event msg: %s\n", job.ResourceName, msg)
			}

			err := feed.EventMsg(msg)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case podName := <-job.AddedPod:
			if debug() {
				fmt.Printf("Job's `%s` pod `%s` added\n", job.ResourceName, podName)
			}

			err := feed.AddedPod(podName)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case chunk := <-job.PodLogChunk:
			if debug() {
				fmt.Printf("Job's `%s` pod `%s` log chunk\n", job.ResourceName, chunk.PodName)
				for _, line := range chunk.LogLines {
					fmt.Printf("[%s] %s\n", line.Timestamp, line.Message)
				}
			}

			err := feed.PodLogChunk(chunk)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case podError := <-job.PodError:
			if debug() {
				fmt.Printf("Job's `%s` pod error: %#v", job.ResourceName, podError)
			}

			err := feed.PodError(podError)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case err := <-errorChan:
			return err

		case <-doneChan:
			return nil
		}
	}
}

type JobTracker struct {
	Tracker

	Added       chan struct{}
	Succeeded   chan struct{}
	Failed      chan string
	EventMsg    chan string
	AddedPod    chan string
	PodLogChunk chan *PodLogChunk
	PodError    chan PodError

	State          TrackerState
	TrackedPods    []string
	FinalJobStatus batchv1.JobStatus

	lastObject     *batchv1.Job
	objectAdded    chan *batchv1.Job
	objectModified chan *batchv1.Job
	objectDeleted  chan *batchv1.Job
	objectFailed   chan string
	podDone        chan string
	errors         chan error
}

func NewJobTracker(ctx context.Context, name, namespace string, kube kubernetes.Interface) *JobTracker {
	return &JobTracker{
		Tracker: Tracker{
			Kube:             kube,
			Namespace:        namespace,
			FullResourceName: fmt.Sprintf("job/%s", name),
			ResourceName:     name,
			Context:          ctx,
		},

		Added:       make(chan struct{}, 0),
		Succeeded:   make(chan struct{}, 0),
		Failed:      make(chan string, 0),
		EventMsg:    make(chan string, 1),
		AddedPod:    make(chan string, 10),
		PodLogChunk: make(chan *PodLogChunk, 1000),
		PodError:    make(chan PodError, 0),

		State:       Initial,
		TrackedPods: make([]string, 0),

		objectAdded:    make(chan *batchv1.Job, 0),
		objectModified: make(chan *batchv1.Job, 0),
		objectDeleted:  make(chan *batchv1.Job, 0),
		objectFailed:   make(chan string, 1),
		podDone:        make(chan string, 10),
		errors:         make(chan error, 0),
	}
}

func (job *JobTracker) Track() error {
	var err error

	err = job.runInformer()
	if err != nil {
		return err
	}

	for {
		select {
		case object := <-job.objectAdded:
			job.lastObject = object
			job.runEventsInformer()

			switch job.State {
			case Initial:
				job.State = ResourceAdded
				job.Added <- struct{}{}

				err = job.runPodsTrackers()
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

			done, err := job.handleJobState(object)
			if err != nil {
				return err
			}
			if done {
				return nil
			}

		case reason := <-job.objectFailed:
			job.State = "Failed"
			job.Failed <- reason

		case <-job.objectDeleted:
			if debug() {
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
			return ErrTrackTimeout

		case err := <-job.errors:
			return err
		}
	}
}

func (job *JobTracker) runInformer() error {
	tweakListOptions := func(options metav1.ListOptions) metav1.ListOptions {
		options.FieldSelector = fields.OneTermEqualSelector("metadata.name", job.ResourceName).String()
		return options
	}
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return job.Kube.Batch().Jobs(job.Namespace).List(tweakListOptions(options))
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return job.Kube.Batch().Jobs(job.Namespace).Watch(tweakListOptions(options))
		},
	}

	go func() {
		_, err := watchtools.UntilWithSync(job.Context, lw, &batchv1.Job{}, nil, func(e watch.Event) (bool, error) {
			if debug() {
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

		if debug() {
			fmt.Printf("Job `%s` informer done\n", job.ResourceName)
		}
	}()

	return nil
}

func (job *JobTracker) handleJobState(object *batchv1.Job) (done bool, err error) {
	if debug() {
		evList, err := utils.ListEventsForObject(job.Kube, object)
		if err != nil {
			return false, err
		}
		utils.DescribeEvents(evList)
	}

	if len(job.TrackedPods) == 0 {
		for _, c := range object.Status.Conditions {
			if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
				job.State = ResourceSucceeded
				job.FinalJobStatus = object.Status
				job.Succeeded <- struct{}{}
				done = true
			} else if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
				job.State = ResourceFailed
				job.Failed <- c.Reason
				done = true
			}
		}
	}

	return
}

func (job *JobTracker) runPodsTrackers() error {
	jobManifest, err := job.Kube.Batch().
		Jobs(job.Namespace).
		Get(job.ResourceName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	selector, err := metav1.LabelSelectorAsSelector(jobManifest.Spec.Selector)
	if err != nil {
		return err
	}

	list, err := job.Kube.Core().Pods(job.Namespace).List(metav1.ListOptions{LabelSelector: selector.String()})
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
			return job.Kube.Core().Pods(job.Namespace).List(tweakListOptions(options))
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return job.Kube.Core().Pods(job.Namespace).Watch(tweakListOptions(options))
		},
	}

	go func() {
		_, err = watchtools.UntilWithSync(job.Context, lw, &corev1.Pod{}, nil, func(e watch.Event) (bool, error) {
			if debug() {
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

		if debug() {
			fmt.Printf("Job `%s` pods informer done\n", job.ResourceName)
		}
	}()

	return nil
}

func (job *JobTracker) runPodTracker(podName string) error {
	errorChan := make(chan error, 0)
	doneChan := make(chan struct{}, 0)

	pod := NewPodTracker(job.Context, podName, job.Namespace, job.Kube)
	job.TrackedPods = append(job.TrackedPods, podName)

	job.AddedPod <- pod.ResourceName

	go func() {
		if debug() {
			fmt.Printf("Starting Job's `%s` Pod `%s` tracker\n", job.ResourceName, pod.ResourceName)
		}

		err := pod.Track()
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- struct{}{}
		}

		if debug() {
			fmt.Printf("Done Job's `%s` Pod `%s` tracker done\n", job.ResourceName, pod.ResourceName)
		}
	}()

	go func() {
		for {
			select {
			case chunk := <-pod.ContainerLogChunk:
				podChunk := &PodLogChunk{ContainerLogChunk: chunk, PodName: pod.ResourceName}
				job.PodLogChunk <- podChunk
			case containerError := <-pod.ContainerError:
				podError := PodError{ContainerError: containerError, PodName: pod.ResourceName}
				job.PodError <- podError
			case msg := <-pod.EventMsg:
				job.EventMsg <- fmt.Sprintf("po/%s %s", pod.ResourceName, msg)
			case <-pod.Added:
			case <-pod.Succeeded:
			case <-pod.Failed:
			case <-pod.Ready:
			case err := <-errorChan:
				job.errors <- err
				return
			case <-doneChan:
				job.podDone <- pod.ResourceName
				return
			}
		}
	}()

	return nil
}

// runEventsInformer watch for DaemonSet events
func (job *JobTracker) runEventsInformer() {
	if job.lastObject == nil {
		return
	}

	eventInformer := NewEventInformer(job.Tracker, job.lastObject)
	eventInformer.WithChannels(job.EventMsg, job.objectFailed, job.errors)
	eventInformer.Run()

	return
}
