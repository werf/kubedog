package multitrack

import (
	"fmt"

	"github.com/flant/kubedog/pkg/tracker/job"
	"github.com/flant/kubedog/pkg/tracker/pod"
	"k8s.io/client-go/kubernetes"
)

func (mt *multitracker) TrackJob(kube kubernetes.Interface, spec MultitrackSpec, opts MultitrackOptions) error {
	feed := job.NewFeed()

	feed.OnAdded(func() error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.JobsStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.jobAdded(spec, feed)
	})
	feed.OnSucceeded(func() error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.JobsStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.jobSucceeded(spec, feed)
	})
	feed.OnFailed(func(reason string) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.JobsStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.jobFailed(spec, feed, reason)
	})
	feed.OnEventMsg(func(msg string) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.JobsStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.jobEventMsg(spec, feed, msg)
	})
	feed.OnAddedPod(func(podName string) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.JobsStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.jobAddedPod(spec, feed, podName)
	})
	feed.OnPodLogChunk(func(chunk *pod.PodLogChunk) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.JobsStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.jobPodLogChunk(spec, feed, chunk)
	})
	feed.OnPodError(func(podError pod.PodError) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.JobsStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.jobPodError(spec, feed, podError)
	})
	feed.OnStatus(func(status job.JobStatus) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.JobsStatuses[spec.ResourceName] = status

		return nil
	})

	return feed.Track(spec.ResourceName, spec.Namespace, kube, opts.Options)
}

func (mt *multitracker) jobAdded(spec MultitrackSpec, feed job.Feed) error {
	mt.displayResourceTrackerMessageF("job", spec, "added")

	return nil
}

func (mt *multitracker) jobSucceeded(spec MultitrackSpec, feed job.Feed) error {
	mt.displayResourceTrackerMessageF("job", spec, "succeeded")

	return mt.handleResourceReadyCondition(mt.TrackingJobs, spec)
}

func (mt *multitracker) jobFailed(spec MultitrackSpec, feed job.Feed, reason string) error {
	mt.displayResourceErrorF("job", spec, "%s", reason)
	return mt.handleResourceFailure(mt.TrackingJobs, "job", spec, reason)
}

func (mt *multitracker) jobEventMsg(spec MultitrackSpec, feed job.Feed, msg string) error {
	mt.displayResourceEventF("job", spec, "%s", msg)
	return nil
}

func (mt *multitracker) jobAddedPod(spec MultitrackSpec, feed job.Feed, podName string) error {
	mt.displayResourceTrackerMessageF("job", spec, "po/%s added", podName)
	return nil
}

func (mt *multitracker) jobPodLogChunk(spec MultitrackSpec, feed job.Feed, chunk *pod.PodLogChunk) error {
	mt.displayResourceLogChunk("job", spec, podContainerLogChunkHeader(chunk.PodName, chunk.ContainerLogChunk), chunk.ContainerLogChunk)
	return nil
}

func (mt *multitracker) jobPodError(spec MultitrackSpec, feed job.Feed, podError pod.PodError) error {
	reason := fmt.Sprintf("po/%s container/%s: %s", podError.PodName, podError.ContainerName, podError.Message)

	mt.displayResourceErrorF("job", spec, "%s", reason)

	return mt.handleResourceFailure(mt.TrackingJobs, "job", spec, reason)
}
