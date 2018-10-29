package monitor

import "k8s.io/client-go/kubernetes"

type DeploymentFeed interface {
	Ready() error
	// ...
}

func MonitorDeployment(name, namespace string, kube kubernetes.Interface, feed DeploymentFeed, opts WatchOptions) error {
	return nil
}
