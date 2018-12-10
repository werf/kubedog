package follow

import (
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/flant/kubedog/pkg/display"
	"github.com/flant/kubedog/pkg/tracker"
)

func TrackPod(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	feed := &tracker.PodFeedProto{
		AddedFunc: func() error {
			fmt.Printf("# po/%s added\n", name)
			return nil
		},
		SucceededFunc: func() error {
			fmt.Printf("# po/%s succeeded\n", name)
			return nil
		},
		FailedFunc: func(reason string) error {
			fmt.Printf("# po/%s failed: %s\n", name, reason)
			return nil
		},
		ReadyFunc: func() error {
			fmt.Printf("# po/%s become READY\n", name)
			return nil
		},
		EventMsgFunc: func(msg string) error {
			fmt.Printf("# po/%s event: %s\n", name, msg)
			return nil
		},
		ContainerErrorFunc: func(containerError tracker.ContainerError) error {
			fmt.Printf("# po/%s %s error: %s\n", name, containerError.ContainerName, containerError.Message)
			return nil
		},
		ContainerLogChunkFunc: func(chunk *tracker.ContainerLogChunk) error {
			header := fmt.Sprintf("po/%s %s", name, chunk.ContainerName)
			display.OutputLogLines(header, chunk.LogLines)
			return nil
		},
	}

	return tracker.TrackPod(name, namespace, kube, feed, opts)
}
