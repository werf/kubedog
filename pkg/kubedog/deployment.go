package kubedog

import (
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/flant/kubedog/pkg/monitor"
)

// DeploymentFeedStub example structure
type DeploymentRolloutStubImpl struct {
}

func (d *DeploymentRolloutStubImpl) Started() error {
	//if debug() {
	fmt.Printf("Deployment rollout is started (new replica set created).")
	//}
	return nil
}

func (d *DeploymentRolloutStubImpl) Succeeded() error {
	//if debug() {
	fmt.Printf("Deployment rollout is succeeded. No action required, just exiting...")
	//}
	return nil
}

func (d *DeploymentRolloutStubImpl) Failed() error {
	//if debug() {
	fmt.Printf("Deployment rollout is succeeded. No action required, just exiting...")
	//}
	return nil
}

// WatchDeploymentTillReady ...
func WatchDeploymentTillReady(name, namespace string, kube kubernetes.Interface) error {
	fmt.Printf("WatchDeploymentTillReady %s %s\n", name, namespace)
	return monitor.WatchDeploymentRollout(name, namespace, kube, &DeploymentRolloutStubImpl{}, monitor.WatchOptions{WaitForResource: true})
}
