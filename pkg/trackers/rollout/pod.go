package rollout

import (
	"fmt"

	"github.com/flant/kubedog/pkg/log"
	"github.com/flant/kubedog/pkg/tracker"
	"k8s.io/client-go/kubernetes"
)

func TrackPod(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	//FIXME: check pod readyness condition and exit

	feed := &tracker.PodFeedProto{
		AddedFunc: func() error {
			fmt.Printf("# Pod `%s` added\n", name)
			return nil
		},
		SucceededFunc: func() error {
			fmt.Printf("# Pod `%s` succeeded\n", name)
			return tracker.StopTrack
		},
		FailedFunc: func() error {
			fmt.Printf("# Pod `%s` failed\n", name)
			return tracker.StopTrack
		},
		ReadyFunc: func() error {
			fmt.Printf("# Pod `%s` ready\n", name)
			return tracker.StopTrack
		},
		ContainerErrorFunc: func(containerError tracker.ContainerError) error {
			fmt.Printf("# Pod `%s` Container `%s` error: %s\n", name, containerError.ContainerName, containerError.Message)
			return nil
		},
		ContainerLogChunkFunc: func(chunk *tracker.ContainerLogChunk) error {
			log.SetLogHeader(fmt.Sprintf("# Pod `%s` Container `%s`", name, chunk.ContainerName))
			for _, line := range chunk.LogLines {
				fmt.Println(line.Data)
			}
			return nil
		},
	}

	return tracker.TrackPod(name, namespace, kube, feed, opts)
}
