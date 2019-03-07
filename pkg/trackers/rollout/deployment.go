package rollout

import (
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/flant/kubedog/pkg/display"
	"github.com/flant/kubedog/pkg/tracker"
)

// TrackDeploymentTillReady ...
func TrackDeploymentTillReady(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	feed := &tracker.ControllerFeedProto{
		AddedFunc: func(ready bool) error {
			if ready {
				fmt.Fprintf(display.Out, "# deploy/%s appears to be ready\n", name)
				return tracker.StopTrack
			}
			fmt.Fprintf(display.Out, "# deploy/%s added\n", name)
			return nil
		},
		ReadyFunc: func() error {
			fmt.Fprintf(display.Out, "# deploy/%s become READY\n", name)
			return tracker.StopTrack
		},
		FailedFunc: func(reason string) error {
			fmt.Fprintf(display.Out, "# deploy/%s FAIL: %s\n", name, reason)
			return tracker.ResourceErrorf("failed: %s", reason)
		},
		EventMsgFunc: func(msg string) error {
			fmt.Fprintf(display.Out, "# deploy/%s event: %s\n", name, msg)
			return nil
		},
		AddedReplicaSetFunc: func(rs tracker.ReplicaSet) error {
			if !rs.IsNew {
				return nil
			}
			fmt.Fprintf(display.Out, "# deploy/%s rs/%s added\n", name, rs.Name)
			return nil
		},
		AddedPodFunc: func(pod tracker.ReplicaSetPod) error {
			if !pod.ReplicaSet.IsNew {
				return nil
			}
			fmt.Fprintf(display.Out, "# deploy/%s po/%s added\n", name, pod.Name)
			return nil
		},
		PodErrorFunc: func(podError tracker.ReplicaSetPodError) error {
			if !podError.ReplicaSet.IsNew {
				return nil
			}
			fmt.Fprintf(display.Out, "# deploy/%s po/%s %s error: %s\n", name, podError.PodName, podError.ContainerName, podError.Message)
			return tracker.ResourceErrorf("deploy/%s po/%s %s failed: %s", name, podError.PodName, podError.ContainerName, podError.Message)
		},
		PodLogChunkFunc: func(chunk *tracker.ReplicaSetPodLogChunk) error {
			if !chunk.ReplicaSet.IsNew {
				return nil
			}
			header := fmt.Sprintf("po/%s %s", chunk.PodName, chunk.ContainerName)
			display.OutputLogLines(header, chunk.LogLines)
			return nil
		},
	}

	err := tracker.TrackDeployment(name, namespace, kube, feed, opts)
	if err != nil {
		switch e := err.(type) {
		case *tracker.ResourceError:
			return e
		default:
			fmt.Fprintf(display.Err, "error tracking Deployment `%s` in namespace `%s`: %s\n", name, namespace, err)
		}
	}
	return err
}
