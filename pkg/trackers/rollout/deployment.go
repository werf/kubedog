package rollout

import (
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/flant/kubedog/pkg/tracker"
)

// DeploymentFeedStub example structure
type DeploymentFeedStubImpl struct {
}

func (d *DeploymentFeedStubImpl) Added(complete bool) error {
	//if debug() {
	//}
	// rollout policy — return if deployment is already completed, do not wait
	if complete {
		fmt.Printf("Deployment is added as complete.\n")
		return tracker.StopTrack
	} else {
		fmt.Printf("Deployment is added.\n")
		return nil
	}
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
	return tracker.StopTrack
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

func (d *DeploymentFeedStubImpl) PodLogChunk(chunk *tracker.PodLogChunk) error {
	//if debug() {
	fmt.Printf("Deployment got new log chunk: %+v\n", chunk)
	//}
	return nil
}

func (d *DeploymentFeedStubImpl) PodError(podError tracker.PodError) error {
	//if debug() {
	fmt.Printf("Deployment got pod error: %+v\n", podError)
	//}
	return nil
}

// TrackDeployment ...
func TrackDeployment(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	fmt.Printf("rollout.TrackDeployment %s %s\n", name, namespace)
	return tracker.TrackDeployment(name, namespace, kube, &DeploymentFeedStubImpl{}, opts)
}
