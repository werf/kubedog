package multitrack

import (
	"fmt"

	"github.com/flant/kubedog/pkg/display"
	"github.com/flant/kubedog/pkg/tracker/job"
	"github.com/flant/kubedog/pkg/tracker/pod"
	"k8s.io/client-go/kubernetes"
)

func (mt *multitracker) TrackJob(kube kubernetes.Interface, spec MultitrackSpec, opts MultitrackOptions) error {
	feed := job.NewFeed()

	feed.OnAdded(func() error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.jobAdded(spec, feed)
	})
	feed.OnSucceeded(func() error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.jobSucceeded(spec, feed)
	})
	feed.OnFailed(func(reason string) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.jobFailed(spec, feed, reason)
	})
	feed.OnEventMsg(func(msg string) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.jobEventMsg(spec, feed, msg)
	})
	feed.OnAddedPod(func(podName string) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.jobAddedPod(spec, feed, podName)
	})
	feed.OnPodLogChunk(func(chunk *pod.PodLogChunk) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.jobPodLogChunk(spec, feed, chunk)
	})
	feed.OnPodError(func(podError pod.PodError) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.jobPodError(spec, feed, podError)
	})
	feed.OnStatusReport(func(status job.JobStatus) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.jobStatusReport(spec, feed, status)
	})

	return feed.Track(spec.ResourceName, spec.Namespace, kube, opts.Options)
}

func (mt *multitracker) jobAdded(spec MultitrackSpec, feed job.Feed) error {
	if debug() {
		fmt.Printf("-- jobAdded %#v\n", spec)
	}

	display.OutF("# job/%s added\n", spec.ResourceName)

	return nil
}

func (mt *multitracker) jobSucceeded(spec MultitrackSpec, feed job.Feed) error {
	if debug() {
		fmt.Printf("-- jobSucceeded %#v\n", spec)
	}

	mt.JobsStatuses[spec.ResourceName] = feed.GetStatus()

	display.OutF("# job/%s succeeded\n", spec.ResourceName)

	return mt.handleResourceReadyCondition(mt.TrackingJobs, spec)
}

func (mt *multitracker) jobFailed(spec MultitrackSpec, feed job.Feed, reason string) error {
	if debug() {
		fmt.Printf("-- jobFailed %#v %#v\n", spec, reason)
	}

	fmt.Fprintf(display.Out, "# job/%s failed: %s\n", spec.ResourceName, reason)

	return mt.handleResourceFailure(mt.TrackingJobs, spec, reason)
}

func (mt *multitracker) jobEventMsg(spec MultitrackSpec, feed job.Feed, msg string) error {
	if debug() {
		fmt.Printf("-- jobEventMsg %#v %#v\n", spec, msg)
	}

	display.OutF("# job/%s event: %s\n", spec.ResourceName, msg)

	return nil
}

func (mt *multitracker) jobAddedPod(spec MultitrackSpec, feed job.Feed, podName string) error {
	if debug() {
		fmt.Printf("-- jobAddedPod %#v %#v\n", spec, podName)
	}

	display.OutF("# job/%s po/%s added\n", spec.ResourceName, podName)

	return nil
}

func (mt *multitracker) jobPodLogChunk(spec MultitrackSpec, feed job.Feed, chunk *pod.PodLogChunk) error {
	if debug() {
		fmt.Printf("-- jobPodLogChunk %#v %#v\n", spec, chunk)
	}

	header := fmt.Sprintf("job/%s po/%s container/%s", spec.ResourceName, chunk.PodName, chunk.ContainerName)
	display.OutputLogLines(header, chunk.LogLines)

	return nil
}

func (mt *multitracker) jobPodError(spec MultitrackSpec, feed job.Feed, podError pod.PodError) error {
	if debug() {
		fmt.Printf("-- jobPodError %#v %#v\n", spec, podError)
	}

	reason := fmt.Sprintf("po/%s container/%s error: %s", podError.PodName, podError.ContainerName, podError.Message)

	fmt.Fprintf(display.Out, "# job/%s %s\n", spec.ResourceName, reason)

	return mt.handleResourceFailure(mt.TrackingJobs, spec, reason)
}

func (mt *multitracker) jobStatusReport(spec MultitrackSpec, feed job.Feed, status job.JobStatus) error {
	if debug() {
		fmt.Printf("-- jobStatusReport %#v %#v\n", spec, status)
	}

	mt.JobsStatuses[spec.ResourceName] = status

	return nil
}
