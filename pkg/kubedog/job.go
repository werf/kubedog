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
			setLogHeader(formatJobHeader(name, chunk.PodName, chunk.ContainerName))
			for _, line := range chunk.LogLines {
				fmt.Println(line.Data)
			}
			return nil
		},
	}

	return monitor.MonitorJob(name, namespace, kube, feed, opts)
}

var currentLogHeader = ""

func setLogHeader(logHeader string) {
	if currentLogHeader != logHeader {
		if currentLogHeader != "" {
			fmt.Println()
		}
		fmt.Printf("%s\n", logHeader)
		currentLogHeader = logHeader
	}
}

func formatJobHeader(jobName, podName, containerName string) string {
	// tail -f on multiple files prints similar headers
	return fmt.Sprintf("==> Job `%s` Pod `%s` container `%s` <==", jobName, podName, containerName)
}
