package monitor

import (
	"context"
	"fmt"
	"time"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	watchtools "k8s.io/client-go/tools/watch"
	"k8s.io/kubernetes/pkg/apis/batch"
	"k8s.io/kubernetes/pkg/apis/core"
)

type JobWatchMonitor struct {
	WatchMonitor

	State string

	Started     chan bool
	Succeeded   chan bool
	Error       chan error
	AddedPod    chan *PodWatchMonitor
	PodLogChunk chan *PodLogChunk
	PodError    chan PodError

	MonitoredPods []*PodWatchMonitor

	FinalJobStatus batch.JobStatus
}

func (job *JobWatchMonitor) Watch() error {
	client := job.Kube

	watcher, err := client.Batch().Jobs(job.Namespace).
		Watch(metav1.ListOptions{
			ResourceVersion: job.InitialResourceVersion,
			Watch:           true,
			FieldSelector:   fields.OneTermEqualSelector("metadata.name", job.ResourceName).String(),
		})

	if err != nil {
		return err
	}

	ctx, cancel := watchtools.ContextWithOptionalTimeout(context.Background(), job.Timeout)
	defer cancel()

	_, err = watchtools.UntilWithoutRetry(ctx, watcher, func(e watch.Event) (bool, error) {
		if debug() {
			fmt.Printf("Job `%s` watcher event: %#v\n", job.ResourceName, e)
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
					err := job.WatchPods()
					if err != nil {
						job.Error <- err
					}
				}()
			}

		case "Started":
			object, ok := e.Object.(*batch.Job)
			if !ok {
				return true, fmt.Errorf("Expected %s to be a *batch.Job, got %T", job.ResourceName, e.Object)
			}

			for _, c := range object.Status.Conditions {
				if c.Type == batch.JobComplete && c.Status == core.ConditionTrue {
					job.State = "Succeeded"

					job.FinalJobStatus = object.Status

					job.Succeeded <- true

					return true, nil
				} else if c.Type == batch.JobFailed && c.Status == core.ConditionTrue {
					job.State = "Failed"

					return true, fmt.Errorf("Job failed: %s", c.Reason)
				}
			}

		default:
			return true, fmt.Errorf("Unknown job `%s` watcher state: %s", job.ResourceName, job.State)
		}

		return false, nil
	})

	return err
}

func (job *JobWatchMonitor) WatchPods() error {
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

	podListWatcher, err := client.Core().
		Pods(job.Namespace).
		Watch(metav1.ListOptions{
			Watch:         true,
			LabelSelector: selector.String(),
		})
	if err != nil {
		return err
	}

	ctx, cancel := watchtools.ContextWithOptionalTimeout(context.Background(), job.Timeout)
	defer cancel()

	_, err = watchtools.UntilWithoutRetry(ctx, podListWatcher, func(e watch.Event) (bool, error) {
		if debug() {
			fmt.Printf("Job `%s` watcher event: %#v\n", job.ResourceName, e)
		}

		podObject, ok := e.Object.(*v1.Pod)
		if !ok {
			return true, fmt.Errorf("Expected %s to be a *v1.Pod, got %T", job.ResourceName, e.Object)
		}
		for _, pod := range job.MonitoredPods {
			if pod.ResourceName == podObject.Name {
				// Already under monitoring
				return false, nil
			}
		}

		// TODO: constructor from job & podObject
		pod := &PodWatchMonitor{
			WatchMonitor: WatchMonitor{
				Kube:    job.Kube,
				Timeout: job.Timeout,

				Namespace:              job.Namespace,
				ResourceName:           podObject.Name,
				InitialResourceVersion: "", // this will make PodWatchMonitor receive podObject again and handle its state properly by itself
			},

			PodLogChunk: job.PodLogChunk,
			PodError:    job.PodError,
			Error:       job.Error,

			ContainerMonitorStates:          make(map[string]string),
			ProcessedContainerLogTimestamps: make(map[string]time.Time),
		}

		for _, containerConf := range podObject.Spec.InitContainers {
			pod.InitContainersNames = append(pod.InitContainersNames, containerConf.Name)
		}
		for _, containerConf := range podObject.Spec.Containers {
			pod.ContainersNames = append(pod.ContainersNames, containerConf.Name)
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

func WatchJobUntilReady(name, namespace string, kube kubernetes.Interface, watchFeed JobWatchFeed, opts WatchOptions) error {
	job := &JobWatchMonitor{
		WatchMonitor: WatchMonitor{
			Kube:    kube,
			Timeout: opts.Timeout,

			Namespace:              namespace,
			ResourceName:           name,
			InitialResourceVersion: opts.InitialResourceVersion,
		},

		Started:     make(chan bool, 0),
		Succeeded:   make(chan bool, 0),
		AddedPod:    make(chan *PodWatchMonitor, 10),
		PodLogChunk: make(chan *PodLogChunk, 1000),

		PodError: make(chan PodError, 0),
		Error:    make(chan error, 0),
	}

	go func() {
		err := job.Watch()
		if err != nil {
			job.Error <- err
		}
	}()

	for {
		select {
		case <-job.Started:
			if debug() {
				fmt.Printf("Job `%s` started\n", job.ResourceName)
			}

			err := watchFeed.Started()
			if err != nil {
				return err
			}

		case <-job.Succeeded:
			if debug() {
				fmt.Printf("Job `%s` succeeded: pods active: %d, pods failed: %d, pods succeeded: %d\n", job.ResourceName, job.FinalJobStatus.Active, job.FinalJobStatus.Failed, job.FinalJobStatus.Succeeded)
			}

			err := watchFeed.Succeeded()
			if err != nil {
				return err
			}

			return nil

		case err := <-job.Error:
			return err

		case pod := <-job.AddedPod:
			if debug() {
				fmt.Printf("Job's `%s` pod `%s` added\n", job.ResourceName, pod.ResourceName)
			}

			err := watchFeed.AddedPod(pod.ResourceName)
			if err != nil {
				return err
			}

		case chunk := <-job.PodLogChunk:
			if debug() {
				fmt.Printf("Job's `%s` pod `%s` log chunk\n", job.ResourceName, chunk.PodName)
			}

			err := watchFeed.LogChunk(JobLogChunk{
				PodLogChunk: *chunk,
				JobName:     job.ResourceName,
			})

			if err != nil {
				return err
			}

		case podError := <-job.PodError:
			if debug() {
				fmt.Printf("Job's `%s` pod error: %#v", job.ResourceName, podError)
			}

			err := watchFeed.PodError(JobPodError{
				JobName:  job.ResourceName,
				PodError: podError,
			})

			if err != nil {
				return err
			}
		}
	}
}
