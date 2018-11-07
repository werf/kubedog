package rollout

import (
	"fmt"

	"github.com/flant/kubedog/pkg/tracker"
	"k8s.io/client-go/kubernetes"
)

// TrackJob
func TrackJob(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	feed := &tracker.JobFeedProto{
		FailedFunc: func(reason string) error {
			fmt.Printf("Job `%s` failed: %s\n", name, reason)
			return nil
		},
		PodErrorFunc: func(podError tracker.PodError) error {
			fmt.Printf("Job `%s` Pod `%s` container `%s` error: %s\n", name, podError.PodName, podError.ContainerName, podError.Message)
			return nil
		},
		PodLogChunkFunc: func(chunk *tracker.PodLogChunk) error {
			// tail -f on multiple files prints similar headers
			setLogHeader(fmt.Sprintf("==> Job `%s` Pod `%s` container `%s` <==", name, chunk.PodName, chunk.ContainerName))
			for _, line := range chunk.LogLines {
				fmt.Println(line.Data)
			}
			return nil
		},
	}

	return tracker.TrackJob(name, namespace, kube, feed, opts)
}
