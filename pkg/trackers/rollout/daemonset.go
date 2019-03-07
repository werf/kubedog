package rollout

import (
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/flant/kubedog/pkg/display"
	"github.com/flant/kubedog/pkg/tracker"
)

// TrackDaemonSetTillReady implements rollout track mode for DaemonSet
//
// Exit on DaemonSet ready or on errors
func TrackDaemonSetTillReady(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	feed := &tracker.ControllerFeedProto{
		AddedFunc: func(ready bool) error {
			if ready {
				fmt.Fprintf(display.Out, "# ds/%s appears to be ready. Exit\n", name)
				return tracker.StopTrack
			}
			fmt.Fprintf(display.Out, "# ds/%s added\n", name)
			return nil
		},
		ReadyFunc: func() error {
			fmt.Fprintf(display.Out, "# ds/%s become READY\n", name)
			return tracker.StopTrack
		},
		FailedFunc: func(reason string) error {
			fmt.Fprintf(display.Err, "# ds/%s FAIL: %s\n", name, reason)
			return tracker.ResourceErrorf("ds/%s failed: %s", name, reason)
		},
		EventMsgFunc: func(msg string) error {
			fmt.Fprintf(display.Out, "# ds/%s event: %s\n", name, msg)
			return nil
		},
		AddedPodFunc: func(pod tracker.ReplicaSetPod) error {
			fmt.Fprintf(display.Out, "# ds/%s po/%s added\n", name, pod.Name)
			return nil
		},
		PodErrorFunc: func(podError tracker.ReplicaSetPodError) error {
			fmt.Fprintf(display.Err, "# ds/%s %s %s error: %s\n", name, podError.PodName, podError.ContainerName, podError.Message)
			return tracker.ResourceErrorf("ds/%s po/%s %s failed: %s", name, podError.PodName, podError.ContainerName, podError.Message)
		},
		PodLogChunkFunc: func(chunk *tracker.ReplicaSetPodLogChunk) error {
			header := fmt.Sprintf("po/%s %s", chunk.PodName, chunk.ContainerName)
			display.OutputLogLines(header, chunk.LogLines)
			return nil
		},
	}

	err := tracker.TrackDaemonSet(name, namespace, kube, feed, opts)
	if err != nil {
		switch e := err.(type) {
		case *tracker.ResourceError:
			return e
		default:
			fmt.Fprintf(display.Err, "error tracking ds/%s in namespace '%s': %s\n", name, namespace, err)
		}
	}
	return err
}
