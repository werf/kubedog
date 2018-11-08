package rollout

import (
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/flant/kubedog/pkg/log"
	"github.com/flant/kubedog/pkg/tracker"
)

// TrackDeployment ...
func TrackDeployment(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	feed := &tracker.DeploymentFeedProto{
		AddedFunc: func(ready bool) error {
			if ready {
				fmt.Printf("# Deployment is added as ready.\n")
				return tracker.StopTrack
			} else {
				fmt.Printf("# Deployment is added.\n")
				return nil
			}
		},
		ReadyFunc: func() error {
			fmt.Printf("# Deployment `%s` ready\n", name)
			return nil
		},
		FailedFunc: func(reason string) error {
			fmt.Printf("# Deployment `%s` failed: %s\n", name, reason)
			return nil
		},
		AddedReplicaSetFunc: func(rsName string) error {
			fmt.Printf("# Deployment `%s` ReplicaSet `%s` added\n", name, rsName)
			return nil
		},
		AddedPodFunc: func(podName string, rsName string, isNew bool) error {
			if isNew {
				fmt.Printf("# Deployment `%s` Pod `%s` added of new ReplicaSet `%s`\n", name, podName, rsName)
			} else {
				fmt.Printf("# Deployment `%s` Pod `%s` added of ReplicaSet `%s`\n", name, podName, rsName)
			}
			return nil
		},
		PodErrorFunc: func(podError tracker.PodError) error {
			fmt.Printf("# Job `%s` Pod `%s` Container `%s` error: %s\n", name, podError.PodName, podError.ContainerName, podError.Message)
			return nil
		},
		PodLogChunkFunc: func(chunk *tracker.PodLogChunk) error {
			log.SetLogHeader(fmt.Sprintf("# Job `%s` Pod `%s` Container `%s`", name, chunk.PodName, chunk.ContainerName))
			for _, line := range chunk.LogLines {
				fmt.Println(line.Data)
			}
			return nil
		},
	}
	return tracker.TrackDeployment(name, namespace, kube, feed, opts)
}
