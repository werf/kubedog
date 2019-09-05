package follow

import (
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/flant/kubedog/pkg/display"
	"github.com/flant/kubedog/pkg/tracker"
	"github.com/flant/kubedog/pkg/tracker/deployment"
	"github.com/flant/kubedog/pkg/tracker/replicaset"
)

func TrackDeployment(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	feed := deployment.NewFeed()

	feed.OnAdded(func(isReady bool) error {
		if isReady {
			fmt.Fprintf(display.Out, "# deploy/%s appears to be ready\n", name)
		} else {
			fmt.Fprintf(display.Out, "# deploy/%s added\n", name)
		}
		return nil
	})
	feed.OnReady(func() error {
		fmt.Fprintf(display.Out, "# deploy/%s become READY\n", name)
		return nil
	})
	feed.OnFailed(func(reason string) error {
		fmt.Fprintf(display.Out, "# deploy/%s FAIL: %s\n", name, reason)
		return nil
	})
	feed.OnEventMsg(func(msg string) error {
		fmt.Fprintf(display.Out, "# deploy/%s event: %s\n", name, msg)
		return nil
	})
	feed.OnAddedReplicaSet(func(rs replicaset.ReplicaSet) error {
		if rs.IsNew {
			fmt.Fprintf(display.Out, "# deploy/%s new rs/%s added\n", name, rs.Name)
		} else {
			fmt.Fprintf(display.Out, "# deploy/%s rs/%s added\n", name, rs.Name)
		}

		return nil
	})
	feed.OnAddedPod(func(pod replicaset.ReplicaSetPod) error {
		if pod.ReplicaSet.IsNew {
			fmt.Fprintf(display.Out, "# deploy/%s rs/%s(new) po/%s added\n", name, pod.ReplicaSet.Name, pod.Name)
		} else {
			fmt.Fprintf(display.Out, "# deploy/%s rs/%s po/%s added\n", name, pod.ReplicaSet.Name, pod.Name)
		}
		return nil
	})
	feed.OnPodError(func(podError replicaset.ReplicaSetPodError) error {
		if podError.ReplicaSet.IsNew {
			fmt.Fprintf(display.Out, "# deploy/%s rs/%s(new) po/%s %s error: %s\n", name, podError.ReplicaSet.Name, podError.PodName, podError.ContainerName, podError.Message)
		} else {
			fmt.Fprintf(display.Out, "# deploy/%s rs/%s po/%s %s error: %s\n", name, podError.ReplicaSet.Name, podError.PodName, podError.ContainerName, podError.Message)
		}
		return nil
	})
	feed.OnPodLogChunk(func(chunk *replicaset.ReplicaSetPodLogChunk) error {
		header := ""
		if chunk.ReplicaSet.IsNew {
			header = fmt.Sprintf("deploy/%s rs/%s(new) po/%s %s", name, chunk.ReplicaSet.Name, chunk.PodName, chunk.ContainerName)
		} else {
			header = fmt.Sprintf("deploy/%s rs/%s po/%s %s", name, chunk.ReplicaSet.Name, chunk.PodName, chunk.ContainerName)
		}
		display.OutputLogLines(header, chunk.LogLines)
		return nil
	})

	return feed.Track(name, namespace, kube, opts)
}
