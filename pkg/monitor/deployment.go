package monitor

type DeploymentWatchFeed interface {
	DeploymentReady() error
	// ...
}

func MonitorDeployment(name, namespace string, kube kubernetes.Interface, wf DeploymentWatchFeed, opts WatchOptions) error {
	// blocking
}
