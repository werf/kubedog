package rollout

import (
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/flant/kubedog/pkg/log"
	"github.com/flant/kubedog/pkg/tracker"
)

// TrackDeploymentTillReady ...
func TrackDeploymentTillReady(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	feed := &tracker.ControllerFeedProto{
		AddedFunc: func(ready bool) error {
			if ready {
				fmt.Printf("# Deployment `%s` ready\n", name)
				return tracker.StopTrack
			}

			fmt.Printf("# Deployment `%s` added\n", name)
			return nil
		},
		ReadyFunc: func() error {
			fmt.Printf("# Deployment `%s` ready\n", name)
			return tracker.StopTrack
		},
		FailedFunc: func(reason string) error {
			fmt.Printf("# Deployment `%s` failed: %s\n", name, reason)
			return fmt.Errorf("failed: %s", reason)
		},
		AddedReplicaSetFunc: func(rs tracker.ReplicaSet) error {
			if !rs.IsNew {
				return nil
			}

			fmt.Printf("# Deployment `%s` current ReplicaSet `%s` added\n", name, rs.Name)

			return nil
		},
		AddedPodFunc: func(pod tracker.ReplicaSetPod) error {
			if !pod.ReplicaSet.IsNew {
				return nil
			}

			fmt.Printf("# Deployment `%s` Pod `%s` added of current ReplicaSet `%s`\n", name, pod.Name, pod.ReplicaSet.Name)

			return nil
		},
		PodErrorFunc: func(podError tracker.ReplicaSetPodError) error {
			if !podError.ReplicaSet.IsNew {
				return nil
			}

			fmt.Printf("# Deployment `%s` Pod `%s` Container `%s` error: %s\n", name, podError.PodName, podError.ContainerName, podError.Message)
			return fmt.Errorf("Pod `%s` Container `%s` failed: %s", name, podError.ContainerName, podError.Message)
		},
		PodLogChunkFunc: func(chunk *tracker.ReplicaSetPodLogChunk) error {
			if !chunk.ReplicaSet.IsNew {
				return nil
			}

			log.SetLogHeader(fmt.Sprintf("# Deployment `%s` Pod `%s` Container `%s` log:", name, chunk.PodName, chunk.ContainerName))
			for _, line := range chunk.LogLines {
				fmt.Println(line.Data)
			}
			return nil
		},
	}
	return tracker.TrackDeployment(name, namespace, kube, feed, opts)
}
