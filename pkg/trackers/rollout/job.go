package rollout

import (
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/werf/kubedog-for-werf-helm/pkg/display"
	"github.com/werf/kubedog-for-werf-helm/pkg/tracker"
	"github.com/werf/kubedog-for-werf-helm/pkg/tracker/job"
	"github.com/werf/kubedog-for-werf-helm/pkg/tracker/pod"
)

func TrackJobTillDone(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	feed := job.NewFeed()

	feed.OnAdded(func() error {
		fmt.Fprintf(display.Out, "# job/%s added\n", name)
		return nil
	})
	feed.OnSucceeded(func() error {
		fmt.Fprintf(display.Out, "# job/%s succeeded\n", name)
		return tracker.ErrStopTrack
	})
	feed.OnFailed(func(reason string) error {
		fmt.Fprintf(display.Out, "# job/%s FAIL: %s\n", name, reason)
		return tracker.ResourceErrorf("failed: %s", reason)
	})
	feed.OnEventMsg(func(msg string) error {
		fmt.Fprintf(display.Out, "# job/%s event: %s\n", name, msg)
		return nil
	})
	feed.OnAddedPod(func(podName string) error {
		fmt.Fprintf(display.Out, "# job/%s po/%s added\n", name, podName)
		return nil
	})
	feed.OnPodLogChunk(func(chunk *pod.PodLogChunk) error {
		header := fmt.Sprintf("po/%s %s", chunk.PodName, chunk.ContainerName)
		display.OutputLogLines(header, chunk.LogLines)
		return nil
	})
	feed.OnPodError(func(podError pod.PodError) error {
		fmt.Fprintf(display.Out, "# job/%s po/%s %s error: %s\n", name, podError.PodName, podError.ContainerName, podError.Message)
		return tracker.ResourceErrorf("job/%s po/%s %s failed: %s", name, podError.PodName, podError.ContainerName, podError.Message)
	})

	err := feed.Track(name, namespace, kube, opts)
	if err != nil {
		switch e := err.(type) {
		case *tracker.ResourceError:
			return e
		default:
			fmt.Fprintf(display.Err, "error tracking job/%s in ns/%s: %s\n", name, namespace, err)
		}
	}
	return err
}
