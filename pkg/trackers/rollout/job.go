package rollout

import (
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/flant/kubedog/pkg/display"
	"github.com/flant/kubedog/pkg/tracker"
	"os"
)

func TrackJobTillDone(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	feed := &tracker.JobFeedProto{
		AddedFunc: func() error {
			fmt.Printf("# job/%s added\n", name)
			return nil
		},
		SucceededFunc: func() error {
			fmt.Printf("# job/%s succeeded\n", name)
			return tracker.StopTrack
		},
		FailedFunc: func(reason string) error {
			fmt.Printf("# job/%s FAIL: %s\n", name, reason)
			return tracker.ResourceErrorf("failed: %s", reason)
		},
		EventMsgFunc: func(msg string) error {
			fmt.Printf("# job/%s event: %s\n", name, msg)
			return nil
		},
		AddedPodFunc: func(podName string) error {
			fmt.Printf("# job/%s po/%s added\n", name, podName)
			return nil
		},
		PodErrorFunc: func(podError tracker.PodError) error {
			fmt.Printf("# job/%s po/%s %s error: %s\n", name, podError.PodName, podError.ContainerName, podError.Message)
			return tracker.ResourceErrorf("job/%s po/%s %s failed: %s", podError.PodName, podError.ContainerName, podError.Message)
		},
		PodLogChunkFunc: func(chunk *tracker.PodLogChunk) error {
			header := fmt.Sprintf("po/%s %s", chunk.PodName, chunk.ContainerName)
			display.OutputLogLines(header, chunk.LogLines)
			return nil
		},
	}

	err := tracker.TrackJob(name, namespace, kube, feed, opts)
	if err != nil {
		switch e := err.(type) {
		case *tracker.ResourceError:
			return e
		default:
			fmt.Fprintf(os.Stderr, "error tracking Job `%s` in namespace `%s`: %s\n", name, namespace, err)
		}
	}
	return err
}
