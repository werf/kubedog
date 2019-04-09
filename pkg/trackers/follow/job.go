package follow

import (
	"fmt"

	"github.com/flant/kubedog/pkg/tracker/job"
	"github.com/flant/kubedog/pkg/tracker/pod"

	"k8s.io/client-go/kubernetes"

	"github.com/flant/kubedog/pkg/display"
	"github.com/flant/kubedog/pkg/tracker"
)

func TrackJob(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	feed := job.NewFeed()

	feed.OnAdded(func() error {
		fmt.Fprintf(display.Out, "# job/%s added\n", name)
		return nil
	})
	feed.OnSucceeded(func() error {
		fmt.Fprintf(display.Out, "# job/%s succeeded\n", name)
		return nil
	})
	feed.OnFailed(func(reason string) error {
		fmt.Fprintf(display.Out, "# job/%s FAIL: %s\n", name, reason)
		return nil
	})
	feed.OnEventMsg(func(msg string) error {
		fmt.Fprintf(display.Out, "# job/%s event: %s\n", name, msg)
		return nil
	})
	feed.OnAddedPod(func(podName string) error {
		fmt.Fprintf(display.Out, "# job/%s po/%s added\n", name, podName)
		return nil
	})
	feed.OnPodError(func(podError pod.PodError) error {
		fmt.Fprintf(display.Out, "# job/%s po/%s %s error: %s\n", name, podError.PodName, podError.ContainerName, podError.Message)
		return nil
	})
	feed.OnPodLogChunk(func(chunk *pod.PodLogChunk) error {
		header := fmt.Sprintf("po/%s %s", chunk.PodName, chunk.ContainerName)
		display.OutputLogLines(header, chunk.LogLines)
		return nil
	})

	return feed.Track(name, namespace, kube, opts)
}
