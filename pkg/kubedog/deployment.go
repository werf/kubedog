package kubedog

import "fmt"

func WatchDeploymentTillReady(name, namespace string) error {
	fmt.Printf("WatchDeploymentTillReady %s %s\n", name, namespace)
	return nil
}
