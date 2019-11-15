package rollout

import (
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/flant/kubedog/pkg/display"
	"github.com/flant/kubedog/pkg/tracker"
	"github.com/flant/kubedog/pkg/tracker/deployment"
	"github.com/flant/kubedog/pkg/tracker/replicaset"
)

// TrackDeploymentTillReady
func TrackDeploymentTillReady(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	feed := deployment.NewFeed()

	feed.OnAdded(func(isReady bool) error {
		if isReady {
			fmt.Fprintf(display.Out, "# deploy/%s appears to be ready\n", name)
			return tracker.StopTrack
		}
		fmt.Fprintf(display.Out, "# deploy/%s added\n", name)
		return nil
	})
	feed.OnReady(func() error {
		fmt.Fprintf(display.Out, "# deploy/%s become READY\n", name)
		return tracker.StopTrack
	})
	feed.OnFailed(func(reason string) error {
		fmt.Fprintf(display.Out, "# deploy/%s FAIL: %s\n", name, reason)
		return tracker.ResourceErrorf("failed: %s", reason)
	})
	feed.OnEventMsg(func(msg string) error {
		fmt.Fprintf(display.Out, "# deploy/%s event: %s\n", name, msg)
		return nil
	})
	feed.OnAddedReplicaSet(func(rs replicaset.ReplicaSet) error {
		if !rs.IsNew {
			return nil
		}
		fmt.Fprintf(display.Out, "# deploy/%s rs/%s added\n", name, rs.Name)
		return nil
	})
	feed.OnAddedPod(func(pod replicaset.ReplicaSetPod) error {
		if !pod.ReplicaSet.IsNew {
			return nil
		}
		fmt.Fprintf(display.Out, "# deploy/%s po/%s added\n", name, pod.Name)
		return nil
	})
	feed.OnPodError(func(podError replicaset.ReplicaSetPodError) error {
		if !podError.ReplicaSet.IsNew {
			return nil
		}
		fmt.Fprintf(display.Out, "# deploy/%s po/%s %s error: %s\n", name, podError.PodName, podError.ContainerName, podError.Message)
		return tracker.ResourceErrorf("deploy/%s po/%s %s failed: %s", name, podError.PodName, podError.ContainerName, podError.Message)
	})
	feed.OnPodLogChunk(func(chunk *replicaset.ReplicaSetPodLogChunk) error {
		if !chunk.ReplicaSet.IsNew {
			return nil
		}
		header := fmt.Sprintf("po/%s %s", chunk.PodName, chunk.ContainerName)
		display.OutputLogLines(header, chunk.LogLines)
		return nil
	})

	err := feed.Track(name, namespace, kube, opts)
	if err != nil {
		switch e := err.(type) {
		case *tracker.ResourceError:
			return e
		default:
			fmt.Fprintf(display.Err, "error tracking deploy/%s in ns/%s: %s\n", name, namespace, err)
		}
	}
	return err
}
