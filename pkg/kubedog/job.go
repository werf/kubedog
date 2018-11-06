package kubedog

import (
	"fmt"

	"github.com/flant/kubedog/pkg/monitor"
	"k8s.io/client-go/kubernetes"
)

func WatchJobTillDone(name, namespace string, kube kubernetes.Interface, opts monitor.WatchOptions) error {
	feed := &monitor.JobFeedProto{
		FailedFunc: func(reason string) error {
			fmt.Printf("Job `%s` failed: %s\n", name, reason)
			return nil
		},
		PodErrorFunc: func(podError monitor.PodError) error {
			fmt.Printf("Job `%s` Pod `%s` container `%s` error: %s\n", name, podError.PodName, podError.ContainerName, podError.Message)
			return nil
		},
		PodLogChunkFunc: func(chunk *monitor.PodLogChunk) error {
			// tail -f on multiple files prints similar headers
			setLogHeader(fmt.Sprintf("==> Job `%s` Pod `%s` container `%s` <==", name, chunk.PodName, chunk.ContainerName))
			for _, line := range chunk.LogLines {
				fmt.Println(line.Data)
			}
			return nil
		},
	}

	return monitor.MonitorJob(name, namespace, kube, feed, opts)
}
