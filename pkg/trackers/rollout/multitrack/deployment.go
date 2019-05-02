package multitrack

import (
	"fmt"

	"github.com/flant/kubedog/pkg/display"
	"github.com/flant/kubedog/pkg/tracker/deployment"
	"github.com/flant/kubedog/pkg/tracker/replicaset"
	"k8s.io/client-go/kubernetes"
)

func (mt *multitracker) TrackDeployment(kube kubernetes.Interface, spec MultitrackSpec, opts MultitrackOptions) error {
	feed := deployment.NewFeed()

	feed.OnAdded(func(ready bool) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.deploymentAdded(spec, feed, ready)
	})
	feed.OnReady(func() error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.deploymentReady(spec, feed)
	})
	feed.OnFailed(func(reason string) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.deploymentFailed(spec, feed, reason)
	})
	feed.OnEventMsg(func(msg string) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.deploymentEventMsg(spec, feed, msg)
	})
	feed.OnAddedReplicaSet(func(rs replicaset.ReplicaSet) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.deploymentAddedReplicaSet(spec, feed, rs)
	})
	feed.OnAddedPod(func(pod replicaset.ReplicaSetPod) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.deploymentAddedPod(spec, feed, pod)
	})
	feed.OnPodError(func(podError replicaset.ReplicaSetPodError) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.deploymentPodError(spec, feed, podError)
	})
	feed.OnPodLogChunk(func(chunk *replicaset.ReplicaSetPodLogChunk) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.deploymentPodLogChunk(spec, feed, chunk)
	})
	feed.OnStatusReport(func(status deployment.DeploymentStatus) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.deploymentStatusReport(spec, feed, status)
	})

	return feed.Track(spec.ResourceName, spec.Namespace, kube, opts.Options)
}

func (mt *multitracker) deploymentAdded(spec MultitrackSpec, feed deployment.Feed, ready bool) error {
	if debug() {
		fmt.Printf("-- deploymentAdded %#v %#v\n", spec, ready)
	}

	if ready {
		mt.DeploymentsStatuses[spec.ResourceName] = feed.GetStatus()

		display.OutF("# deploy/%s appears to be READY\n", spec.ResourceName)

		return mt.handleResourceReadyCondition(mt.TrackingDeployments, spec)
	}

	display.OutF("# deploy/%s added\n", spec.ResourceName)

	return nil
}

func (mt *multitracker) deploymentReady(spec MultitrackSpec, feed deployment.Feed) error {
	if debug() {
		fmt.Printf("-- deploymentReady %#v\n", spec)
	}

	mt.DeploymentsStatuses[spec.ResourceName] = feed.GetStatus()

	display.OutF("# deploy/%s become READY\n", spec.ResourceName)

	return mt.handleResourceReadyCondition(mt.TrackingDeployments, spec)
}

func (mt *multitracker) deploymentFailed(spec MultitrackSpec, feed deployment.Feed, reason string) error {
	if debug() {
		fmt.Printf("-- deploymentFailed %#v %#v\n", spec, reason)
	}

	display.OutF("# deploy/%s FAIL: %s\n", spec.ResourceName, reason)

	return mt.handleResourceFailure(mt.TrackingDeployments, spec, reason)
}

func (mt *multitracker) deploymentEventMsg(spec MultitrackSpec, feed deployment.Feed, msg string) error {
	if debug() {
		fmt.Printf("-- deploymentEventMsg %#v %#v\n", spec, msg)
	}

	display.OutF("# deploy/%s event: %s\n", spec.ResourceName, msg)

	return nil
}

func (mt *multitracker) deploymentAddedReplicaSet(spec MultitrackSpec, feed deployment.Feed, rs replicaset.ReplicaSet) error {
	if debug() {
		fmt.Printf("-- deploymentAddedReplicaSet %#v %#v\n", spec, rs)
	}

	if !rs.IsNew {
		return nil
	}
	display.OutF("# deploy/%s rs/%s added\n", spec.ResourceName, rs.Name)

	return nil
}

func (mt *multitracker) deploymentAddedPod(spec MultitrackSpec, feed deployment.Feed, pod replicaset.ReplicaSetPod) error {
	if debug() {
		fmt.Printf("-- deploymentAddedPod %#v %#v\n", spec, pod)
	}

	if !pod.ReplicaSet.IsNew {
		return nil
	}
	display.OutF("# deploy/%s po/%s added\n", spec.ResourceName, pod.Name)

	return nil
}

func (mt *multitracker) deploymentPodError(spec MultitrackSpec, feed deployment.Feed, podError replicaset.ReplicaSetPodError) error {
	if debug() {
		fmt.Printf("-- deploymentPodError %#v %#v\n", spec, podError)
	}

	if !podError.ReplicaSet.IsNew {
		return nil
	}

	reason := fmt.Sprintf("po/%s container/%s error: %s", podError.PodName, podError.ContainerName, podError.Message)

	display.OutF("# deploy/%s %s\n", spec.ResourceName, reason)

	return mt.handleResourceFailure(mt.TrackingDeployments, spec, reason)
}

func (mt *multitracker) deploymentPodLogChunk(spec MultitrackSpec, feed deployment.Feed, chunk *replicaset.ReplicaSetPodLogChunk) error {
	if debug() {
		fmt.Printf("-- deploymentPodLogChunk %#v %#v\n", spec, chunk)
	}

	if !chunk.ReplicaSet.IsNew {
		return nil
	}

	header := fmt.Sprintf("deploy/%s %s", spec.ResourceName, podContainerLogChunkHeader(chunk.PodName, chunk.ContainerLogChunk))
	displayContainerLogChunk(header, spec, chunk.ContainerLogChunk)

	return nil
}

func (mt *multitracker) deploymentStatusReport(spec MultitrackSpec, feed deployment.Feed, status deployment.DeploymentStatus) error {
	if debug() {
		fmt.Printf("-- deploymentStatusReport %#v %#v\n", spec, status)
	}

	mt.DeploymentsStatuses[spec.ResourceName] = status

	return nil
}
