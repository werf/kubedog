package kubedog

import (
	"fmt"

	"github.com/flant/kubedog/pkg/monitor"
	"k8s.io/client-go/kubernetes"
)

func WatchPodTillDone(name, namespace string, kube kubernetes.Interface, opts monitor.WatchOptions) error {
	feed := &monitor.PodFeedProto{
		AddedFunc: func() error {
			fmt.Printf("Pod `%s` added\n", name)
			return nil
		},
		SucceededFunc: func() error {
			fmt.Printf("Pod `%s` succeeded\n", name)
			return nil
		},
		FailedFunc: func() error {
			fmt.Printf("Pod `%s` failed\n", name)
			return nil
		},
		ContainerErrorFunc: func(containerError monitor.ContainerError) error {
			fmt.Printf("Pod `%s` container `%s` error: %s\n", name, containerError.ContainerName, containerError.Message)
			return nil
		},
		ContainerLogChunkFunc: func(chunk *monitor.ContainerLogChunk) error {
			setLogHeader(fmt.Sprintf("==> Pod `%s` container `%s` <==", name, chunk.ContainerName))
			for _, line := range chunk.LogLines {
				fmt.Println(line.Data)
			}
			return nil
		},
	}

	return monitor.MonitorPod(name, namespace, kube, feed, opts)
}
