package multitrack

import (
	"fmt"

	"github.com/flant/kubedog/pkg/tracker/deployment"
	"github.com/flant/kubedog/pkg/tracker/replicaset"
	"k8s.io/client-go/kubernetes"
)

func (mt *multitracker) TrackDeployment(kube kubernetes.Interface, spec MultitrackSpec, opts MultitrackOptions) error {
	feed := deployment.NewFeed()

	feed.OnAdded(func(ready bool) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()
		return mt.deploymentAdded(spec, feed, ready)
	})
	feed.OnReady(func() error {
		mt.mux.Lock()
		defer mt.mux.Unlock()
		return mt.deploymentReady(spec, feed)
	})
	feed.OnFailed(func(reason string) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()
		return mt.deploymentFailed(spec, feed, reason)
	})
	feed.OnEventMsg(func(msg string) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()
		return mt.deploymentEventMsg(spec, feed, msg)
	})
	feed.OnAddedReplicaSet(func(rs replicaset.ReplicaSet) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()
		return mt.deploymentAddedReplicaSet(spec, feed, rs)
	})
	feed.OnAddedPod(func(pod replicaset.ReplicaSetPod) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()
		return mt.deploymentAddedPod(spec, feed, pod)
	})
	feed.OnPodError(func(podError replicaset.ReplicaSetPodError) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()
		return mt.deploymentPodError(spec, feed, podError)
	})
	feed.OnPodLogChunk(func(chunk *replicaset.ReplicaSetPodLogChunk) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()
		return mt.deploymentPodLogChunk(spec, feed, chunk)
	})
	feed.OnStatusReport(func(status deployment.DeploymentStatus) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()
		return mt.deploymentStatusReport(spec, feed, status)
	})

	return feed.Track(spec.ResourceName, spec.Namespace, kube, opts.Options)
}

func (mt *multitracker) deploymentAdded(spec MultitrackSpec, feed deployment.Feed, ready bool) error {
	if ready {
		mt.DeploymentsStatuses[spec.ResourceName] = feed.GetStatus()

		mt.displayResourceTrackerMessageF("deploy", spec.ResourceName, "appears to be READY\n")

		return mt.handleResourceReadyCondition(mt.TrackingDeployments, spec)
	}

	mt.displayResourceTrackerMessageF("deploy", spec.ResourceName, "added\n")

	return nil
}

func (mt *multitracker) deploymentReady(spec MultitrackSpec, feed deployment.Feed) error {
	mt.DeploymentsStatuses[spec.ResourceName] = feed.GetStatus()

	mt.displayResourceTrackerMessageF("deploy", spec.ResourceName, "become READY\n")

	return mt.handleResourceReadyCondition(mt.TrackingDeployments, spec)
}

func (mt *multitracker) deploymentFailed(spec MultitrackSpec, feed deployment.Feed, reason string) error {
	mt.displayResourceErrorF("deploy", spec.ResourceName, "%s\n", reason)
	return mt.handleResourceFailure(mt.TrackingDeployments, "deploy", spec, reason)
}

func (mt *multitracker) deploymentEventMsg(spec MultitrackSpec, feed deployment.Feed, msg string) error {
	mt.displayResourceEventF("deploy", spec.ResourceName, "%s\n", msg)
	return nil
}

func (mt *multitracker) deploymentAddedReplicaSet(spec MultitrackSpec, feed deployment.Feed, rs replicaset.ReplicaSet) error {
	if !rs.IsNew {
		return nil
	}

	mt.displayResourceTrackerMessageF("deploy", spec.ResourceName, "rs/%s added\n", rs.Name)

	return nil
}

func (mt *multitracker) deploymentAddedPod(spec MultitrackSpec, feed deployment.Feed, pod replicaset.ReplicaSetPod) error {
	if !pod.ReplicaSet.IsNew {
		return nil
	}

	mt.displayResourceTrackerMessageF("deploy", spec.ResourceName, "po/%s added\n", pod.Name)

	return nil
}

func (mt *multitracker) deploymentPodError(spec MultitrackSpec, feed deployment.Feed, podError replicaset.ReplicaSetPodError) error {
	if !podError.ReplicaSet.IsNew {
		return nil
	}

	reason := fmt.Sprintf("po/%s container/%s: %s", podError.PodName, podError.ContainerName, podError.Message)

	mt.displayResourceErrorF("deploy", spec.ResourceName, "%s\n", reason)

	return mt.handleResourceFailure(mt.TrackingDeployments, "deploy", spec, reason)
}

func (mt *multitracker) deploymentPodLogChunk(spec MultitrackSpec, feed deployment.Feed, chunk *replicaset.ReplicaSetPodLogChunk) error {
	if !chunk.ReplicaSet.IsNew {
		return nil
	}

	mt.displayResourceLogChunk("deploy", spec.ResourceName, podContainerLogChunkHeader(chunk.PodName, chunk.ContainerLogChunk), spec, chunk.ContainerLogChunk)

	return nil
}

func (mt *multitracker) deploymentStatusReport(spec MultitrackSpec, feed deployment.Feed, status deployment.DeploymentStatus) error {
	mt.DeploymentsStatuses[spec.ResourceName] = status
	return nil
}
