package rollout

import (
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/werf/kubedog/pkg/display"
	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/tracker/pod"
)

func TrackPodTillReady(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	feed := pod.NewFeed()

	feed.OnAdded(func() error {
		fmt.Fprintf(display.Out, "# po/%s added\n", name)
		return nil
	})
	feed.OnSucceeded(func() error {
		fmt.Fprintf(display.Out, "# po/%s succeeded\n", name)
		return tracker.StopTrack
	})
	feed.OnFailed(func(reason string) error {
		fmt.Fprintf(display.Out, "# po/%s failed: %s\n", name, reason)
		return tracker.ResourceErrorf("po/%s failed: %s", name, reason)
	})
	feed.OnReady(func() error {
		fmt.Fprintf(display.Out, "# po/%s become READY\n", name)
		return tracker.StopTrack
	})
	feed.OnEventMsg(func(msg string) error {
		fmt.Fprintf(display.Out, "# po/%s event: %s\n", name, msg)
		return nil
	})
	feed.OnContainerError(func(containerError pod.ContainerError) error {
		fmt.Fprintf(display.Out, "# po/%s %s error: %s\n", name, containerError.ContainerName, containerError.Message)
		return tracker.ResourceErrorf("po/%s %s failed: %s", name, containerError.ContainerName, containerError.Message)
	})
	feed.OnContainerLogChunk(func(chunk *pod.ContainerLogChunk) error {
		header := fmt.Sprintf("po/%s %s", name, chunk.ContainerName)
		display.OutputLogLines(header, chunk.LogLines)
		return nil
	})

	err := feed.Track(name, namespace, kube, opts)
	if err != nil {
		switch e := err.(type) {
		case *tracker.ResourceError:
			return e
		default:
			fmt.Fprintf(display.Err, "error tracking po/%s in ns/%s: %s\n", name, namespace, err)
		}
	}
	return err
}
