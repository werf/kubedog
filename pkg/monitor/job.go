package monitor

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
)

type JobFeed interface {
	Added() error
	Succeeded() error
	Failed(reason string) error
	AddedPod(podName string) error
	PodLogChunk(*PodLogChunk) error
	PodError(PodError) error
}

type PodLogChunk struct {
	*ContainerLogChunk
	PodName string
}

type PodError struct {
	ContainerError
	PodName string
}

func MonitorJob(name, namespace string, kube kubernetes.Interface, feed JobFeed, opts WatchOptions) error {
	errorChan := make(chan error, 0)
	doneChan := make(chan bool, 0)

	parentContext := opts.ParentContext
	if parentContext == nil {
		parentContext = context.Background()
	}
	ctx, cancel := watchtools.ContextWithOptionalTimeout(parentContext, opts.Timeout)
	defer cancel()

	job := NewJobWatchMonitor(ctx, name, namespace, kube)

	go func() {
		err := job.Watch()
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- true
		}
	}()

	for {
		select {
		case <-job.Added:
			if debug() {
				fmt.Printf("Job `%s` added\n", job.ResourceName)
			}

			err := feed.Added()
			if err == StopWatch {
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
			if err == StopWatch {
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
			if err == StopWatch {
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
			if err == StopWatch {
				return nil
			}
			if err != nil {
				return err
			}

		case chunk := <-job.PodLogChunk:
			if debug() {
				fmt.Printf("Job's `%s` pod `%s` log chunk\n", job.ResourceName, chunk.PodName)
				for _, line := range chunk.LogLines {
					fmt.Printf("[%s] %s\n", line.Timestamp, line.Data)
				}
			}

			err := feed.PodLogChunk(chunk)
			if err == StopWatch {
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
			if err == StopWatch {
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

type JobWatchMonitor struct {
	WatchMonitor

	Added       chan bool
	Succeeded   chan bool
	Failed      chan string
	AddedPod    chan string
	PodLogChunk chan *PodLogChunk
	PodError    chan PodError

	State          WatchMonitorState
	MonitoredPods  []string
	FinalJobStatus batchv1.JobStatus

	lastObject     *batchv1.Job
	objectAdded    chan *batchv1.Job
	objectModified chan *batchv1.Job
	objectDeleted  chan *batchv1.Job
	podDone        chan string
	errors         chan error
}

func NewJobWatchMonitor(ctx context.Context, name, namespace string, kube kubernetes.Interface) *JobWatchMonitor {
	return &JobWatchMonitor{
		WatchMonitor: WatchMonitor{
			Kube:         kube,
			Namespace:    namespace,
			ResourceName: name,
			Context:      ctx,
		},

		Added:       make(chan bool, 0),
		Succeeded:   make(chan bool, 0),
		Failed:      make(chan string, 0),
		AddedPod:    make(chan string, 10),
		PodLogChunk: make(chan *PodLogChunk, 1000),
		PodError:    make(chan PodError, 0),

		State:         WatchInitial,
		MonitoredPods: make([]string, 0),

		objectAdded:    make(chan *batchv1.Job, 0),
		objectModified: make(chan *batchv1.Job, 0),
		objectDeleted:  make(chan *batchv1.Job, 0),
		podDone:        make(chan string, 10),
		errors:         make(chan error, 0),
	}
}

func (job *JobWatchMonitor) Watch() error {
	var err error

	err = job.runInformer()
	if err != nil {
		return err
	}

	for {
		select {
		case object := <-job.objectAdded:
			job.lastObject = object

			switch job.State {
			case WatchInitial:
				job.State = ResourceAdded
				job.Added <- true

				err = job.runPodsWatcher()
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

		case <-job.objectDeleted:
			if debug() {
				fmt.Printf("Job `%s` resource gone: stop watching\n", job.ResourceName)
			}
			return nil

		case podName := <-job.podDone:
			monitoredPods := make([]string, 0)
			for _, name := range job.MonitoredPods {
				if name != podName {
					monitoredPods = append(monitoredPods, name)
				}
			}
			job.MonitoredPods = monitoredPods

			done, err := job.handleJobState(job.lastObject)
			if err != nil {
				return err
			}
			if done {
				return nil
			}

		case <-job.Context.Done():
			return ErrWatchTimeout

		case err := <-job.errors:
			return err
		}
	}
}

func (job *JobWatchMonitor) runInformer() error {
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

func (job *JobWatchMonitor) handleJobState(object *batchv1.Job) (done bool, err error) {
	if len(job.MonitoredPods) == 0 {
		for _, c := range object.Status.Conditions {
			if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
				job.State = ResourceSucceeded
				job.FinalJobStatus = object.Status
				job.Succeeded <- true
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

func (job *JobWatchMonitor) runPodsWatcher() error {
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
		err := job.runPodWatcher(item.Name)
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
				for _, podName := range job.MonitoredPods {
					if podName == object.Name {
						// Already under monitored
						return false, nil
					}
				}

				err := job.runPodWatcher(object.Name)
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

func (job *JobWatchMonitor) runPodWatcher(podName string) error {
	errorChan := make(chan error, 0)
	doneChan := make(chan struct{}, 0)

	pod := NewPodWatchMonitor(job.Context, podName, job.Namespace, job.Kube)
	job.MonitoredPods = append(job.MonitoredPods, podName)

	job.AddedPod <- pod.ResourceName

	go func() {
		if debug() {
			fmt.Printf("Starting Job's `%s` Pod `%s` monitor\n", job.ResourceName, pod.ResourceName)
		}

		err := pod.Watch()
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- struct{}{}
		}

		if debug() {
			fmt.Printf("Done Job's `%s` Pod `%s` monitor done\n", job.ResourceName, pod.ResourceName)
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
			case <-pod.Succeeded:
			case <-pod.Failed:
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
