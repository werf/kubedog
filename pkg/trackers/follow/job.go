package follow

import (
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/flant/kubedog/pkg/display"
	"github.com/flant/kubedog/pkg/tracker"
)

func TrackJob(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	feed := &tracker.JobFeedProto{
		AddedFunc: func() error {
			fmt.Fprintf(display.Out, "# job/%s added\n", name)
			return nil
		},
		SucceededFunc: func() error {
			fmt.Fprintf(display.Out, "# job/%s succeeded\n", name)
			return nil
		},
		FailedFunc: func(reason string) error {
			fmt.Fprintf(display.Out, "# job/%s FAIL: %s\n", name, reason)
			return nil
		},
		EventMsgFunc: func(msg string) error {
			fmt.Fprintf(display.Out, "# job/%s event: %s\n", name, msg)
			return nil
		},
		AddedPodFunc: func(podName string) error {
			fmt.Fprintf(display.Out, "# job/%s po/%s added\n", name, podName)
			return nil
		},
		PodErrorFunc: func(podError tracker.PodError) error {
			fmt.Fprintf(display.Out, "# job/%s po/%s %s error: %s\n", name, podError.PodName, podError.ContainerName, podError.Message)
			return nil
		},
		PodLogChunkFunc: func(chunk *tracker.PodLogChunk) error {
			header := fmt.Sprintf("po/%s %s", chunk.PodName, chunk.ContainerName)
			display.OutputLogLines(header, chunk.LogLines)
			return nil
		},
	}

	return tracker.TrackJob(name, namespace, kube, feed, opts)
}
