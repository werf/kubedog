package kubedog

import (
	"github.com/flant/kubedog/pkg/monitor"
	"k8s.io/client-go/kubernetes"
)

func WatchJobTillDone(name, namespace string, kube kubernetes.Interface) error {
	return monitor.WatchJobUntilReady("test-job", "myns", kube, monitor.JobWatchFeedStub, monitor.WatchOptions{})
}
