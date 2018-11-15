package follow

import (
	"fmt"

	"github.com/flant/kubedog/pkg/log"
	"github.com/flant/kubedog/pkg/tracker"
	"k8s.io/client-go/kubernetes"
)

func TrackDeployment(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	feed := &tracker.DeploymentFeedProto{
		AddedFunc: func(ready bool) error {
			if ready {
				fmt.Printf("# Deployment `%s` added (ready)\n", name)
			} else {
				fmt.Printf("# Deployment `%s` added\n", name)
			}
			return nil
		},
		ReadyFunc: func() error {
			fmt.Printf("# Deployment `%s` ready\n", name)
			return nil
		},
		FailedFunc: func(reason string) error {
			fmt.Printf("# Deployment `%s` failed: %s\n", name, reason)
			return nil
		},
		AddedReplicaSetFunc: func(rs tracker.ReplicaSet) error {
			if rs.IsNew {
				fmt.Printf("# New Deployment `%s` ReplicaSet `%s` added\n", name, rs.Name)
			} else {
				fmt.Printf("# Deployment `%s` ReplicaSet `%s` added\n", name, rs.Name)
			}

			return nil
		},
		AddedPodFunc: func(pod tracker.ReplicaSetPod) error {
			if pod.ReplicaSet.IsNew {
				fmt.Printf("# New Deployment `%s` Pod `%s` added\n", name, pod.Name)
			} else {
				fmt.Printf("# Deployment `%s` Pod `%s` added\n", name, pod.Name)
			}
			return nil
		},
		PodErrorFunc: func(podError tracker.ReplicaSetPodError) error {
			if podError.ReplicaSet.IsNew {
				fmt.Printf("# New Deployment `%s` Pod `%s` Container `%s` error: %s\n", name, podError.PodName, podError.ContainerName, podError.Message)
			} else {
				fmt.Printf("# Deployment `%s` Pod `%s` Container `%s` error: %s\n", name, podError.PodName, podError.ContainerName, podError.Message)
			}
			return nil
		},
		PodLogChunkFunc: func(chunk *tracker.ReplicaSetPodLogChunk) error {
			if chunk.ReplicaSet.IsNew {
				log.SetLogHeader(fmt.Sprintf("# New Deployment `%s` Pod `%s` Container `%s` logs:", name, chunk.PodName, chunk.ContainerName))
			} else {
				log.SetLogHeader(fmt.Sprintf("# Deployment `%s` Pod `%s` Container `%s` logs:", name, chunk.PodName, chunk.ContainerName))
			}
			for _, line := range chunk.LogLines {
				fmt.Println(line.Data)
			}
			return nil
		},
	}

	return tracker.TrackDeployment(name, namespace, kube, feed, opts)
}
