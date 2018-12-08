package follow

import (
	"fmt"

	"github.com/flant/kubedog/pkg/log"
	"github.com/flant/kubedog/pkg/tracker"
	"k8s.io/client-go/kubernetes"
)

func TrackDaemonSet(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	feed := &tracker.ControllerFeedProto{
		AddedFunc: func(ready bool) error {
			if ready {
				fmt.Printf("ds/%s appears to be ready\n", name)
			} else {
				fmt.Printf("ds/%s added\n", name)
			}
			return nil
		},
		ReadyFunc: func() error {
			fmt.Printf("ds/%s become READY\n", name)
			return nil
		},
		FailedFunc: func(reason string) error {
			fmt.Printf("ds/%s FAIL: %s\n", name, reason)
			return nil
		},
		AddedPodFunc: func(pod tracker.ReplicaSetPod) error {
			fmt.Printf("+ ds/%s %s\n", name, pod.Name)
			return nil
		},
		PodErrorFunc: func(podError tracker.ReplicaSetPodError) error {
			fmt.Printf("ds/%s %s %s error: %s\n", name, podError.PodName, podError.ContainerName, podError.Message)
			return nil
		},
		PodLogChunkFunc: func(chunk *tracker.ReplicaSetPodLogChunk) error {
			log.SetLogHeader(fmt.Sprintf("ds/%s %s %s:", name, chunk.PodName, chunk.ContainerName))
			for _, line := range chunk.LogLines {
				fmt.Println(line.Data)
			}
			return nil
		},
	}

	return tracker.TrackDaemonSet(name, namespace, kube, feed, opts)
}
