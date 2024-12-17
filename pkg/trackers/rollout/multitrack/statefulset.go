package multitrack

import (
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/werf/kubedog-for-werf-helm/pkg/tracker/replicaset"
	"github.com/werf/kubedog-for-werf-helm/pkg/tracker/statefulset"
)

func (mt *multitracker) TrackStatefulSet(kube kubernetes.Interface, spec MultitrackSpec, opts MultitrackOptions) error {
	feed := statefulset.NewFeed()

	feed.OnAdded(func(isReady bool) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.StatefulSetsStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.statefulsetAdded(spec, feed, isReady)
	})
	feed.OnReady(func() error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.StatefulSetsStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.statefulsetReady(spec, feed)
	})
	feed.OnFailed(func(reason string) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.StatefulSetsStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.statefulsetFailed(spec, feed, reason)
	})
	feed.OnEventMsg(func(msg string) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.StatefulSetsStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.statefulsetEventMsg(spec, feed, msg)
	})
	feed.OnAddedReplicaSet(func(rs replicaset.ReplicaSet) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.StatefulSetsStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.statefulsetAddedReplicaSet(spec, feed, rs)
	})
	feed.OnAddedPod(func(pod replicaset.ReplicaSetPod) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.StatefulSetsStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.statefulsetAddedPod(spec, feed, pod)
	})
	feed.OnPodError(func(podError replicaset.ReplicaSetPodError) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.StatefulSetsStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.statefulsetPodError(spec, feed, podError)
	})
	feed.OnPodLogChunk(func(chunk *replicaset.ReplicaSetPodLogChunk) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.StatefulSetsStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.statefulsetPodLogChunk(spec, feed, chunk)
	})
	feed.OnStatus(func(status statefulset.StatefulSetStatus) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.StatefulSetsStatuses[spec.ResourceName] = status

		return nil
	})

	return feed.Track(spec.ResourceName, spec.Namespace, kube, opts.Options)
}

func (mt *multitracker) statefulsetAdded(spec MultitrackSpec, feed statefulset.Feed, isReady bool) error {
	if isReady {
		mt.displayResourceTrackerMessageF("sts", spec.ResourceName, spec.ShowServiceMessages, "appears to be READY")

		return mt.handleResourceReadyCondition(mt.TrackingStatefulSets, spec)
	}

	mt.displayResourceTrackerMessageF("sts", spec.ResourceName, spec.ShowServiceMessages, "added")

	return nil
}

func (mt *multitracker) statefulsetReady(spec MultitrackSpec, feed statefulset.Feed) error {
	mt.displayResourceTrackerMessageF("sts", spec.ResourceName, spec.ShowServiceMessages, "become READY")

	return mt.handleResourceReadyCondition(mt.TrackingStatefulSets, spec)
}

func (mt *multitracker) statefulsetFailed(spec MultitrackSpec, feed statefulset.Feed, reason string) error {
	mt.displayResourceErrorF("sts", spec.ResourceName, "%s", reason)
	return mt.handleResourceFailure(mt.TrackingStatefulSets, "sts", spec, reason)
}

func (mt *multitracker) statefulsetEventMsg(spec MultitrackSpec, feed statefulset.Feed, msg string) error {
	mt.displayResourceEventF("sts", spec.ResourceName, spec.ShowServiceMessages, "%s", msg)
	return nil
}

func (mt *multitracker) statefulsetAddedReplicaSet(spec MultitrackSpec, feed statefulset.Feed, rs replicaset.ReplicaSet) error {
	mt.displayResourceTrackerMessageF("sts", spec.ResourceName, spec.ShowServiceMessages, "rs/%s added", rs.Name)
	return nil
}

func (mt *multitracker) statefulsetAddedPod(spec MultitrackSpec, feed statefulset.Feed, pod replicaset.ReplicaSetPod) error {
	mt.displayResourceTrackerMessageF("sts", spec.ResourceName, spec.ShowServiceMessages, "po/%s added", pod.Name)
	return nil
}

func (mt *multitracker) statefulsetPodError(spec MultitrackSpec, feed statefulset.Feed, podError replicaset.ReplicaSetPodError) error {
	reason := fmt.Sprintf("po/%s container/%s: %s", podError.PodName, podError.ContainerName, podError.Message)

	mt.displayResourceErrorF("sts", spec.ResourceName, "%s", reason)

	return mt.handleResourceFailure(mt.TrackingStatefulSets, "sts", spec, reason)
}

func (mt *multitracker) statefulsetPodLogChunk(spec MultitrackSpec, feed statefulset.Feed, chunk *replicaset.ReplicaSetPodLogChunk) error {
	status := mt.StatefulSetsStatuses[spec.ResourceName]
	if podStatus, hasKey := status.Pods[chunk.PodName]; hasKey {
		if podStatus.IsReady {
			return nil
		}
	}

	mt.displayResourceLogChunk("sts", spec, podContainerLogChunkHeader(chunk.PodName, chunk.ContainerLogChunk), chunk.ContainerLogChunk)
	return nil
}
