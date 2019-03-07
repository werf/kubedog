package rollout

import (
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/flant/kubedog/pkg/display"
	"github.com/flant/kubedog/pkg/tracker"
)

func TrackPodTillReady(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	feed := &tracker.PodFeedProto{
		AddedFunc: func() error {
			fmt.Fprintf(display.Out, "# po/%s added\n", name)
			return nil
		},
		SucceededFunc: func() error {
			fmt.Fprintf(display.Out, "# po/%s succeeded\n", name)
			return tracker.StopTrack
		},
		FailedFunc: func(reason string) error {
			fmt.Fprintf(display.Out, "# po/%s failed: %s\n", name, reason)
			return tracker.ResourceErrorf("po/%s failed: %s", name, reason)
		},
		ReadyFunc: func() error {
			fmt.Fprintf(display.Out, "# po/%s become READY\n", name)
			return tracker.StopTrack
		},
		EventMsgFunc: func(msg string) error {
			fmt.Fprintf(display.Out, "# po/%s event: %s\n", name, msg)
			return nil
		},
		ContainerErrorFunc: func(containerError tracker.ContainerError) error {
			fmt.Fprintf(display.Out, "# po/%s %s error: %s\n", name, containerError.ContainerName, containerError.Message)
			return tracker.ResourceErrorf("po/%s %s failed: %s", name, containerError.ContainerName, containerError.Message)
		},
		ContainerLogChunkFunc: func(chunk *tracker.ContainerLogChunk) error {
			header := fmt.Sprintf("po/%s %s", name, chunk.ContainerName)
			display.OutputLogLines(header, chunk.LogLines)
			return nil
		},
	}

	err := tracker.TrackPod(name, namespace, kube, feed, opts)
	if err != nil {
		switch e := err.(type) {
		case *tracker.ResourceError:
			return e
		default:
			fmt.Fprintf(display.Err, "error tracking Pod `%s` in namespace `%s`: %s\n", name, namespace, err)
		}
	}
	return err
}
