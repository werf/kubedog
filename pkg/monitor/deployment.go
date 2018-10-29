package monitor

import "k8s.io/client-go/kubernetes"

type DeploymentWatchFeed interface {
	DeploymentReady() error
	// ...
}

func MonitorDeployment(name, namespace string, kube kubernetes.Interface, wf DeploymentWatchFeed, opts WatchOptions) error {
	return nil
}
