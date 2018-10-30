package kubedog

import (
	"github.com/flant/kubedog/pkg/monitor"
	"k8s.io/client-go/kubernetes"
)

func WatchPod(name, namespace string, kube kubernetes.Interface) error {
	return monitor.MonitorPod(name, namespace, kube, monitor.PodFeedStub, monitor.WatchOptions{})
}
