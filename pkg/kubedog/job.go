package kubedog

import "fmt"

func WatchJobTillDone(name, namespace string) error {
	fmt.Printf("WatchJobTillDone %s %s\n", name, namespace)
	return nil
}
