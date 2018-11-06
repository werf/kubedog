package kubedog

import (
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/flant/kubedog/pkg/monitor"
)

// DeploymentFeedStub example structure
type DeploymentFeedStubImpl struct {
}

func (d *DeploymentFeedStubImpl) Added() error {
	//if debug() {
	fmt.Printf("Deployment is added.\n")
	//}
	return nil
}

func (d *DeploymentFeedStubImpl) Completed() error {
	//if debug() {
	fmt.Printf("Deployment is completed. No action required, just exiting...\n")
	//}
	return nil
}

//
func (d *DeploymentFeedStubImpl) Failed(reason string) error {
	//if debug() {
	fmt.Printf("Deployment is failed: %v\n", reason)
	//}
	return monitor.StopWatch
}

func (d *DeploymentFeedStubImpl) AddedReplicaSet(rsName string) error {
	//if debug() {
	fmt.Printf("Deployment got new ReplicaSet `%s`\n", rsName)
	//}
	return nil
}

//
func (d *DeploymentFeedStubImpl) AddedPod(podName string, rsName string, isRsNew bool) error {
	//if debug() {
	fmt.Printf("Deployment got new pod: %s. rs/%s, new: %v\n", podName, rsName, isRsNew)
	//}
	return nil
}

func (d *DeploymentFeedStubImpl) PodLogChunk(chunk *monitor.PodLogChunk) error {
	//if debug() {
	fmt.Printf("Deployment got new log chunk: %+v\n", chunk)
	//}
	return nil
}

func (d *DeploymentFeedStubImpl) PodError(podError monitor.PodError) error {
	//if debug() {
	fmt.Printf("Deployment got pod error: %+v\n", podError)
	//}
	return nil
}

// WatchDeploymentTillReady ...
func WatchDeploymentTillReady(name, namespace string, kube kubernetes.Interface) error {
	fmt.Printf("WatchDeploymentTillReady %s %s\n", name, namespace)
	return monitor.WatchDeploymentRollout(name, namespace, kube, &DeploymentFeedStubImpl{}, monitor.WatchOptions{WaitForResource: true})
}
