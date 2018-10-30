package monitor

import (
	"context"
	"fmt"
	"time"

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
	Started() error
	Succeeded() error
	Failed(reason string) error
	AddedPod(podName string) error
	LogChunk(JobLogChunk) error
	PodError(JobPodError) error
}

type JobLogChunk struct {
	ContainerLogChunk
	PodName string
	JobName string
}

type JobPodError struct {
	PodError
	JobName string
}

func MonitorJob(name, namespace string, kube kubernetes.Interface, feed JobFeed, opts WatchOptions) error {
	job := &JobWatchMonitor{
		WatchMonitor: WatchMonitor{
			Kube:         kube,
			Timeout:      opts.Timeout,
			Namespace:    namespace,
			ResourceName: name,
		},

		Started:           make(chan bool, 0),
		Succeeded:         make(chan bool, 0),
		AddedPod:          make(chan *PodWatchMonitor, 10),
		ContainerLogChunk: make(chan *ContainerLogChunk, 1000),

		PodError: make(chan PodError, 0),
		Error:    make(chan error, 0),
	}

	go func() {
		err := job.Watch()
		if err != nil {
			job.Error <- err
		}
	}()

	// TODO: err code to interrupt select
	for {
		select {
		case <-job.Started:
			if debug() {
				fmt.Printf("Job `%s` started\n", job.ResourceName)
			}

			err := feed.Started()
			if err != nil {
				return err
			}

		case <-job.Succeeded:
			if debug() {
				fmt.Printf("Job `%s` succeeded: pods failed: %d, pods succeeded: %d\n", job.ResourceName, job.FinalJobStatus.Failed, job.FinalJobStatus.Succeeded)
			}
			// return feed.Succeeded()

		case reason := <-job.FailedReason:
			if debug() {
				fmt.Printf("Job `%s` failed: %s\n", job.ResourceName, reason)
			}
			// return feed.Failed(reason)

		case err := <-job.Error:
			return err

		case pod := <-job.AddedPod:
			if debug() {
				fmt.Printf("Job's `%s` pod `%s` added\n", job.ResourceName, pod.ResourceName)
			}

			err := feed.AddedPod(pod.ResourceName)
			if err != nil {
				return err
			}

		case chunk := <-job.ContainerLogChunk:
			if debug() {
				fmt.Printf("Job's `%s` pod `%s` log chunk\n", job.ResourceName)
			}

			err := feed.LogChunk(JobLogChunk{
				ContainerLogChunk: *chunk,
				JobName:           job.ResourceName,
			})
			if err != nil {
				return err
			}

		case podError := <-job.PodError:
			if debug() {
				fmt.Printf("Job's `%s` pod error: %#v", job.ResourceName, podError)
			}

			err := feed.PodError(JobPodError{
				JobName:  job.ResourceName,
				PodError: podError,
			})
			if err != nil {
				return err
			}
		}
	}
}

type JobWatchMonitor struct {
	WatchMonitor

	State string

	Started           chan bool
	Succeeded         chan bool
	FailedReason      chan string
	Error             chan error
	AddedPod          chan *PodWatchMonitor
	ContainerLogChunk chan *ContainerLogChunk
	PodError          chan PodError

	MonitoredPods []*PodWatchMonitor

	FinalJobStatus batchv1.JobStatus
}

func (job *JobWatchMonitor) Watch() error {
	client := job.Kube

	tweakListOptions := func(options metav1.ListOptions) metav1.ListOptions {
		options.FieldSelector = fields.OneTermEqualSelector("metadata.name", job.ResourceName).String()
		return options
	}
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return client.Batch().Jobs(job.Namespace).List(tweakListOptions(options))
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return client.Batch().Jobs(job.Namespace).Watch(tweakListOptions(options))
		},
	}

	ctx, cancel := watchtools.ContextWithOptionalTimeout(context.Background(), job.Timeout)
	defer cancel()
	_, err := watchtools.UntilWithSync(ctx, lw, &batchv1.Job{}, nil, func(e watch.Event) (bool, error) {
		if debug() {
			fmt.Printf("Job `%s` watcher: %#v\n", job.ResourceName, e.Type)
		}

		object, ok := e.Object.(*batchv1.Job)
		if !ok {
			return true, fmt.Errorf("expected %s to be a *batchv1.Job, got %T", job.ResourceName, e.Object)
		}

		switch job.State {
		case "":
			if e.Type == watch.Added {
				job.Started <- true

				job.State = "Started"

				if debug() {
					fmt.Printf("Starting to watch job `%s` pods\n", job.ResourceName)
				}

				go func() {
					err := job.watchPods()
					if err != nil {
						job.Error <- err
					}
				}()
			}
			return job.handleStatusConditions(object)

		case "Started":
			return job.handleStatusConditions(object)

		default:
			return true, fmt.Errorf("unknown job `%s` watcher state: %s", job.ResourceName, job.State)
		}
	})

	return err
}

func (job *JobWatchMonitor) handleStatusConditions(object *batchv1.Job) (watchDone bool, err error) {
	for _, c := range object.Status.Conditions {
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			job.State = "Succeeded"
			job.FinalJobStatus = object.Status
			job.Succeeded <- true

			return true, nil
		} else if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			job.State = "Failed"
			job.FailedReason <- c.Reason

			return true, nil
		}
	}

	return false, nil
}

func (job *JobWatchMonitor) watchPods() error {
	client := job.Kube

	jobManifest, err := client.Batch().
		Jobs(job.Namespace).
		Get(job.ResourceName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	selector, err := metav1.LabelSelectorAsSelector(jobManifest.Spec.Selector)
	if err != nil {
		return err
	}

	tweakListOptions := func(options metav1.ListOptions) metav1.ListOptions {
		options.LabelSelector = selector.String()
		return options
	}
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return client.Core().Pods(job.Namespace).List(tweakListOptions(options))
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return client.Core().Pods(job.Namespace).Watch(tweakListOptions(options))
		},
	}

	ctx, cancel := watchtools.ContextWithOptionalTimeout(context.Background(), job.Timeout)
	defer cancel()
	_, err = watchtools.UntilWithSync(ctx, lw, &corev1.Pod{}, nil, func(e watch.Event) (bool, error) {
		if debug() {
			fmt.Printf("Job `%s` pods watcher: %#v\n", job.ResourceName, e.Type)
		}

		podObject, ok := e.Object.(*corev1.Pod)
		if !ok {
			return true, fmt.Errorf("Expected %s to be a *corev1.Pod, got %T", job.ResourceName, e.Object)
		}

		for _, pod := range job.MonitoredPods {
			if pod.ResourceName == podObject.Name {
				// Already under monitoring
				return false, nil
			}
		}

		pod := &PodWatchMonitor{
			WatchMonitor: WatchMonitor{
				Kube:         job.Kube,
				Timeout:      job.Timeout,
				Namespace:    job.Namespace,
				ResourceName: podObject.Name,
			},

			ContainerLogChunk: job.ContainerLogChunk,
			PodError:          job.PodError,
			Error:             job.Error,

			ContainerMonitorStates:          make(map[string]string),
			ProcessedContainerLogTimestamps: make(map[string]time.Time),
		}

		job.MonitoredPods = append(job.MonitoredPods, pod)

		if debug() {
			fmt.Printf("Starting job's `%s` pod `%s` monitor\n", job.ResourceName, pod.ResourceName)
		}

		go func() {
			err := pod.Watch()
			if err != nil {
				job.Error <- err
			}
		}()

		job.AddedPod <- pod

		return false, nil
	})

	return err
}
