package rollout

import (
	"fmt"

	"github.com/flant/kubedog/pkg/log"
	"github.com/flant/kubedog/pkg/tracker"
	"k8s.io/client-go/kubernetes"
)

func TrackJobTillDone(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	feed := &tracker.JobFeedProto{
		AddedFunc: func() error {
			fmt.Printf("# Job `%s` added\n", name)
			return nil
		},
		SucceededFunc: func() error {
			fmt.Printf("# Job `%s` succeeded\n", name)
			return tracker.StopTrack
		},
		FailedFunc: func(reason string) error {
			fmt.Printf("# Job `%s` failed: %s\n", name, reason)
			return fmt.Errorf("failed: %s", reason)
		},
		AddedPodFunc: func(podName string) error {
			fmt.Printf("# Job `%s` Pod `%s` added\n", name, podName)
			return nil
		},
		PodErrorFunc: func(podError tracker.PodError) error {
			fmt.Printf("# Job `%s` Pod `%s` Container `%s` error: %s\n", name, podError.PodName, podError.ContainerName, podError.Message)
			return fmt.Errorf("Pod `%s` Container `%s` failed: %s", podError.PodName, podError.ContainerName, podError.Message)
		},
		PodLogChunkFunc: func(chunk *tracker.PodLogChunk) error {
			log.SetLogHeader(fmt.Sprintf("# Job `%s` Pod `%s` Container `%s` log:", name, chunk.PodName, chunk.ContainerName))
			for _, line := range chunk.LogLines {
				fmt.Println(line.Data)
			}
			return nil
		},
	}

	return tracker.TrackJob(name, namespace, kube, feed, opts)
}
