package multitrack

import (
	"fmt"

	"github.com/flant/kubedog/pkg/tracker/deployment"
	"github.com/flant/kubedog/pkg/tracker/replicaset"
	"k8s.io/client-go/kubernetes"
)

func (mt *multitracker) TrackDeployment(kube kubernetes.Interface, spec MultitrackSpec, opts MultitrackOptions) error {
	feed := deployment.NewFeed()

	feed.OnAdded(func(isReady bool) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.DeploymentsStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.deploymentAdded(spec, feed, isReady)
	})
	feed.OnReady(func() error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.DeploymentsStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.deploymentReady(spec, feed)
	})
	feed.OnFailed(func(reason string) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.DeploymentsStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.deploymentFailed(spec, feed, reason)
	})
	feed.OnEventMsg(func(msg string) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.DeploymentsStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.deploymentEventMsg(spec, feed, msg)
	})
	feed.OnAddedReplicaSet(func(rs replicaset.ReplicaSet) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.DeploymentsStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.deploymentAddedReplicaSet(spec, feed, rs)
	})
	feed.OnAddedPod(func(pod replicaset.ReplicaSetPod) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.DeploymentsStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.deploymentAddedPod(spec, feed, pod)
	})
	feed.OnPodError(func(podError replicaset.ReplicaSetPodError) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.DeploymentsStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.deploymentPodError(spec, feed, podError)
	})
	feed.OnPodLogChunk(func(chunk *replicaset.ReplicaSetPodLogChunk) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.DeploymentsStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.deploymentPodLogChunk(spec, feed, chunk)
	})
	feed.OnStatus(func(status deployment.DeploymentStatus) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.DeploymentsStatuses[spec.ResourceName] = status

		return nil
	})

	return feed.Track(spec.ResourceName, spec.Namespace, kube, opts.Options)
}

func (mt *multitracker) deploymentAdded(spec MultitrackSpec, feed deployment.Feed, isReady bool) error {
	if isReady {
		mt.displayResourceTrackerMessageF("deploy", spec, "appears to be READY")

		return mt.handleResourceReadyCondition(mt.TrackingDeployments, spec)
	}

	mt.displayResourceTrackerMessageF("deploy", spec, "added")

	return nil
}

func (mt *multitracker) deploymentReady(spec MultitrackSpec, feed deployment.Feed) error {
	mt.displayResourceTrackerMessageF("deploy", spec, "become READY")

	return mt.handleResourceReadyCondition(mt.TrackingDeployments, spec)
}

func (mt *multitracker) deploymentFailed(spec MultitrackSpec, feed deployment.Feed, reason string) error {
	mt.displayResourceErrorF("deploy", spec, "%s", reason)

	return mt.handleResourceFailure(mt.TrackingDeployments, "deploy", spec, reason)
}

func (mt *multitracker) deploymentEventMsg(spec MultitrackSpec, feed deployment.Feed, msg string) error {
	mt.displayResourceEventF("deploy", spec, "%s", msg)
	return nil
}

func (mt *multitracker) deploymentAddedReplicaSet(spec MultitrackSpec, feed deployment.Feed, rs replicaset.ReplicaSet) error {
	if !rs.IsNew {
		return nil
	}

	mt.displayResourceTrackerMessageF("deploy", spec, "rs/%s added", rs.Name)

	return nil
}

func (mt *multitracker) deploymentAddedPod(spec MultitrackSpec, feed deployment.Feed, pod replicaset.ReplicaSetPod) error {
	if !pod.ReplicaSet.IsNew {
		return nil
	}

	mt.displayResourceTrackerMessageF("deploy", spec, "po/%s added", pod.Name)

	return nil
}

func (mt *multitracker) deploymentPodError(spec MultitrackSpec, feed deployment.Feed, podError replicaset.ReplicaSetPodError) error {
	if !podError.ReplicaSet.IsNew {
		return nil
	}

	reason := fmt.Sprintf("po/%s container/%s: %s", podError.PodName, podError.ContainerName, podError.Message)

	mt.displayResourceErrorF("deploy", spec, "%s", reason)

	return mt.handleResourceFailure(mt.TrackingDeployments, "deploy", spec, reason)
}

func (mt *multitracker) deploymentPodLogChunk(spec MultitrackSpec, feed deployment.Feed, chunk *replicaset.ReplicaSetPodLogChunk) error {
	if !chunk.ReplicaSet.IsNew {
		return nil
	}

	status := mt.DeploymentsStatuses[spec.ResourceName]
	if podStatus, hasKey := status.Pods[chunk.PodName]; hasKey {
		if podStatus.IsReady {
			return nil
		}
	}

	mt.displayResourceLogChunk("deploy", spec, podContainerLogChunkHeader(chunk.PodName, chunk.ContainerLogChunk), chunk.ContainerLogChunk)

	return nil
}
