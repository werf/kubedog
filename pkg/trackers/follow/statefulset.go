package follow

import (
	"fmt"

	"github.com/flant/kubedog/pkg/tracker/replicaset"
	"github.com/flant/kubedog/pkg/tracker/statefulset"

	"k8s.io/client-go/kubernetes"

	"github.com/flant/kubedog/pkg/display"
	"github.com/flant/kubedog/pkg/tracker"
)

func TrackStatefulSet(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	feed := statefulset.NewFeed()

	feed.OnAdded(func(isReady bool) error {
		if isReady {
			fmt.Fprintf(display.Out, "# sts/%s appears to be ready\n", name)
		} else {
			fmt.Fprintf(display.Out, "# sts/%s added\n", name)
		}
		return nil
	})
	feed.OnReady(func() error {
		fmt.Fprintf(display.Out, "# sts/%s become READY\n", name)
		return nil
	})
	feed.OnFailed(func(reason string) error {
		fmt.Fprintf(display.Out, "# sts/%s FAIL: %s\n", name, reason)
		return nil
	})
	feed.OnEventMsg(func(msg string) error {
		fmt.Fprintf(display.Out, "# sts/%s event: %s\n", name, msg)
		return nil
	})
	feed.OnAddedPod(func(pod replicaset.ReplicaSetPod) error {
		fmt.Fprintf(display.Out, "# sts/%s po/%s added\n", name, pod.Name)
		return nil
	})
	feed.OnPodError(func(podError replicaset.ReplicaSetPodError) error {
		fmt.Fprintf(display.Out, "# sts/%s %s %s error: %s\n", name, podError.PodName, podError.ContainerName, podError.Message)
		return nil
	})
	feed.OnPodLogChunk(func(chunk *replicaset.ReplicaSetPodLogChunk) error {
		header := fmt.Sprintf("po/%s %s", chunk.PodName, chunk.ContainerName)
		display.OutputLogLines(header, chunk.LogLines)
		return nil
	})

	return feed.Track(name, namespace, kube, opts)
}
