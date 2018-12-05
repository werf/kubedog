package rollout

import (
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/flant/kubedog/pkg/log"
	"github.com/flant/kubedog/pkg/tracker"
)

// TrackStatefulSetTillReady implements rollout track mode for StatefulSet
//
// Exit on DaemonSet ready or on errors
func TrackStatefulSetTillReady(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	feed := &tracker.ControllerFeedProto{
		AddedFunc: func(ready bool) error {
			if ready {
				fmt.Printf("sts/%s appears to be ready. Exit\n", name)
				return tracker.StopTrack
			}

			fmt.Printf("sts/%s added\n", name)
			return nil
		},
		ReadyFunc: func() error {
			fmt.Printf("sts/%s become READY\n", name)
			return tracker.StopTrack
		},
		FailedFunc: func(reason string) error {
			fmt.Printf("sts/%s FAIL: %s\n", name, reason)
			return fmt.Errorf("failed: %s", reason)
		},
		AddedPodFunc: func(pod tracker.ReplicaSetPod) error {
			fmt.Printf("+ sts/%s %s\n", name, pod.Name)
			return nil
		},
		PodErrorFunc: func(podError tracker.ReplicaSetPodError) error {
			fmt.Printf("sts/%s %s %s error: %s\n", name, podError.PodName, podError.ContainerName, podError.Message)
			return fmt.Errorf("sts/%s %s %s failed: %s", name, podError.PodName, podError.ContainerName, podError.Message)
		},
		PodLogChunkFunc: func(chunk *tracker.ReplicaSetPodLogChunk) error {
			log.SetLogHeader(fmt.Sprintf("sts/%s %s %s:", name, chunk.PodName, chunk.ContainerName))
			for _, line := range chunk.LogLines {
				fmt.Println(line.Data)
			}
			return nil
		},
	}
	return tracker.TrackStatefulSet(name, namespace, kube, feed, opts)
}
