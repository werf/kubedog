package follow

import (
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/flant/kubedog/pkg/display"
	"github.com/flant/kubedog/pkg/tracker"
)

func TrackDaemonSet(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	feed := &tracker.ControllerFeedProto{
		AddedFunc: func(ready bool) error {
			if ready {
				fmt.Printf("# ds/%s appears to be ready\n", name)
			} else {
				fmt.Printf("# ds/%s added\n", name)
			}
			return nil
		},
		ReadyFunc: func() error {
			fmt.Printf("# ds/%s become READY\n", name)
			return nil
		},
		EventMsgFunc: func(msg string) error {
			fmt.Printf("# ds/%s event: %s\n", name, msg)
			return nil
		},
		FailedFunc: func(reason string) error {
			fmt.Printf("# ds/%s FAIL: %s\n", name, reason)
			return nil
		},
		AddedPodFunc: func(pod tracker.ReplicaSetPod) error {
			fmt.Printf("# ds/%s po/%s added\n", name, pod.Name)
			return nil
		},
		PodErrorFunc: func(podError tracker.ReplicaSetPodError) error {
			fmt.Printf("# ds/%s %s %s error: %s\n", name, podError.PodName, podError.ContainerName, podError.Message)
			return nil
		},
		PodLogChunkFunc: func(chunk *tracker.ReplicaSetPodLogChunk) error {
			header := fmt.Sprintf("po/%s %s", chunk.PodName, chunk.ContainerName)
			display.OutputLogLines(header, chunk.LogLines)
			return nil
		},
	}

	return tracker.TrackDaemonSet(name, namespace, kube, feed, opts)
}
