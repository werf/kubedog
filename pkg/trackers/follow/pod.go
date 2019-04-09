package follow

import (
	"fmt"

	"github.com/flant/kubedog/pkg/tracker/pod"

	"k8s.io/client-go/kubernetes"

	"github.com/flant/kubedog/pkg/display"
	"github.com/flant/kubedog/pkg/tracker"
)

func TrackPod(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	feed := pod.NewFeed()

	feed.OnAdded(func() error {
		fmt.Fprintf(display.Out, "# po/%s added\n", name)
		return nil
	})
	feed.OnSucceeded(func() error {
		fmt.Fprintf(display.Out, "# po/%s succeeded\n", name)
		return nil
	})
	feed.OnFailed(func(reason string) error {
		fmt.Fprintf(display.Out, "# po/%s failed: %s\n", name, reason)
		return nil
	})
	feed.OnReady(func() error {
		fmt.Fprintf(display.Out, "# po/%s become READY\n", name)
		return nil
	})
	feed.OnEventMsg(func(msg string) error {
		fmt.Fprintf(display.Out, "# po/%s event: %s\n", name, msg)
		return nil
	})
	feed.OnContainerError(func(containerError pod.ContainerError) error {
		fmt.Fprintf(display.Out, "# po/%s %s error: %s\n", name, containerError.ContainerName, containerError.Message)
		return nil
	})
	feed.OnContainerLogChunk(func(chunk *pod.ContainerLogChunk) error {
		header := fmt.Sprintf("po/%s %s", name, chunk.ContainerName)
		display.OutputLogLines(header, chunk.LogLines)
		return nil
	})

	return feed.Track(name, namespace, kube, opts)
}
