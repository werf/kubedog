package rollout

import (
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/flant/kubedog/pkg/display"
	"github.com/flant/kubedog/pkg/tracker"
	"github.com/flant/kubedog/pkg/tracker/daemonset"
	"github.com/flant/kubedog/pkg/tracker/replicaset"
)

// TrackDaemonSetTillReady implements rollout track mode for DaemonSet
//
// Exit on DaemonSet ready or on errors
func TrackDaemonSetTillReady(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	feed := daemonset.NewFeed()

	feed.OnAdded(func(isReady bool) error {
		if isReady {
			fmt.Fprintf(display.Out, "# ds/%s appears to be ready. Exit\n", name)
			return tracker.StopTrack
		}
		fmt.Fprintf(display.Out, "# ds/%s added\n", name)
		return nil
	})
	feed.OnReady(func() error {
		fmt.Fprintf(display.Out, "# ds/%s become READY\n", name)
		return tracker.StopTrack
	})
	feed.OnFailed(func(reason string) error {
		fmt.Fprintf(display.Err, "# ds/%s FAIL: %s\n", name, reason)
		return tracker.ResourceErrorf("ds/%s failed: %s", name, reason)
	})
	feed.OnEventMsg(func(msg string) error {
		fmt.Fprintf(display.Out, "# ds/%s event: %s\n", name, msg)
		return nil
	})
	feed.OnAddedPod(func(pod replicaset.ReplicaSetPod) error {
		fmt.Fprintf(display.Out, "# ds/%s po/%s added\n", name, pod.Name)
		return nil
	})
	feed.OnPodError(func(podError replicaset.ReplicaSetPodError) error {
		fmt.Fprintf(display.Err, "# ds/%s %s %s error: %s\n", name, podError.PodName, podError.ContainerName, podError.Message)
		return tracker.ResourceErrorf("ds/%s po/%s %s failed: %s", name, podError.PodName, podError.ContainerName, podError.Message)
	})
	feed.OnPodLogChunk(func(chunk *replicaset.ReplicaSetPodLogChunk) error {
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
			fmt.Fprintf(display.Err, "error tracking ds/%s in ns/%s: %s\n", name, namespace, err)
		}
	}
	return err
}
