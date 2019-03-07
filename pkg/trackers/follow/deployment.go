package follow

import (
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/flant/kubedog/pkg/display"
	"github.com/flant/kubedog/pkg/tracker"
)

func TrackDeployment(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	feed := &tracker.ControllerFeedProto{
		AddedFunc: func(ready bool) error {
			if ready {
				fmt.Fprintf(display.Out, "# deploy/%s appears to be ready\n", name)
			} else {
				fmt.Fprintf(display.Out, "# deploy/%s added\n", name)
			}
			return nil
		},
		ReadyFunc: func() error {
			fmt.Fprintf(display.Out, "# deploy/%s become READY\n", name)
			return nil
		},
		FailedFunc: func(reason string) error {
			fmt.Fprintf(display.Out, "# deploy/%s FAIL: %s\n", name, reason)
			return nil
		},
		EventMsgFunc: func(msg string) error {
			fmt.Fprintf(display.Out, "# deploy/%s event: %s\n", name, msg)
			return nil
		},
		AddedReplicaSetFunc: func(rs tracker.ReplicaSet) error {
			if rs.IsNew {
				fmt.Fprintf(display.Out, "# deploy/%s new rs/%s added\n", name, rs.Name)
			} else {
				fmt.Fprintf(display.Out, "# deploy/%s rs/%s added\n", name, rs.Name)
			}

			return nil
		},
		AddedPodFunc: func(pod tracker.ReplicaSetPod) error {
			if pod.ReplicaSet.IsNew {
				fmt.Fprintf(display.Out, "# deploy/%s rs/%s(new) po/%s added\n", name, pod.ReplicaSet.Name, pod.Name)
			} else {
				fmt.Fprintf(display.Out, "# deploy/%s rs/%s po/%s added\n", name, pod.ReplicaSet.Name, pod.Name)
			}
			return nil
		},
		PodErrorFunc: func(podError tracker.ReplicaSetPodError) error {
			if podError.ReplicaSet.IsNew {
				fmt.Fprintf(display.Out, "# deploy/%s rs/%s(new) po/%s %s error: %s\n", name, podError.ReplicaSet.Name, podError.PodName, podError.ContainerName, podError.Message)
			} else {
				fmt.Fprintf(display.Out, "# deploy/%s rs/%s po/%s %s error: %s\n", name, podError.ReplicaSet.Name, podError.PodName, podError.ContainerName, podError.Message)
			}
			return nil
		},
		PodLogChunkFunc: func(chunk *tracker.ReplicaSetPodLogChunk) error {
			header := ""
			if chunk.ReplicaSet.IsNew {
				header = fmt.Sprintf("deploy/%s rs/%s(new) po/%s %s", name, chunk.ReplicaSet.Name, chunk.PodName, chunk.ContainerName)
			} else {
				header = fmt.Sprintf("deploy/%s rs/%s po/%s %s", name, chunk.ReplicaSet.Name, chunk.PodName, chunk.ContainerName)
			}
			display.OutputLogLines(header, chunk.LogLines)
			return nil
		},
	}

	return tracker.TrackDeployment(name, namespace, kube, feed, opts)
}
