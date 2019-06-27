package multitrack

import (
	"fmt"

	"github.com/flant/kubedog/pkg/tracker/replicaset"
	"github.com/flant/kubedog/pkg/tracker/statefulset"
	"k8s.io/client-go/kubernetes"
)

func (mt *multitracker) TrackStatefulSet(kube kubernetes.Interface, spec MultitrackSpec, opts MultitrackOptions) error {
	feed := statefulset.NewFeed()

	feed.OnAdded(func(ready bool) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.statefulsetAdded(spec, feed, ready)
	})
	feed.OnReady(func() error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.statefulsetReady(spec, feed)
	})
	feed.OnFailed(func(reason string) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.statefulsetFailed(spec, feed, reason)
	})
	feed.OnEventMsg(func(msg string) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.statefulsetEventMsg(spec, feed, msg)
	})
	feed.OnAddedReplicaSet(func(rs replicaset.ReplicaSet) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.statefulsetAddedReplicaSet(spec, feed, rs)
	})
	feed.OnAddedPod(func(pod replicaset.ReplicaSetPod) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.statefulsetAddedPod(spec, feed, pod)
	})
	feed.OnPodError(func(podError replicaset.ReplicaSetPodError) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.statefulsetPodError(spec, feed, podError)
	})
	feed.OnPodLogChunk(func(chunk *replicaset.ReplicaSetPodLogChunk) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.statefulsetPodLogChunk(spec, feed, chunk)
	})
	feed.OnStatusReport(func(status statefulset.StatefulSetStatus) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.statefulsetStatusReport(spec, feed, status)
	})

	return feed.Track(spec.ResourceName, spec.Namespace, kube, opts.Options)
}

func (mt *multitracker) statefulsetAdded(spec MultitrackSpec, feed statefulset.Feed, ready bool) error {
	if ready {
		mt.StatefulSetsStatuses[spec.ResourceName] = feed.GetStatus()

		mt.displayResourceTrackerMessageF("sts", spec.ResourceName, "appears to be READY\n")

		return mt.handleResourceReadyCondition(mt.TrackingStatefulSets, spec)
	}

	mt.displayResourceTrackerMessageF("sts", spec.ResourceName, "added\n")

	return nil
}

func (mt *multitracker) statefulsetReady(spec MultitrackSpec, feed statefulset.Feed) error {
	mt.StatefulSetsStatuses[spec.ResourceName] = feed.GetStatus()

	mt.displayResourceTrackerMessageF("sts", spec.ResourceName, "become READY\n")

	return mt.handleResourceReadyCondition(mt.TrackingStatefulSets, spec)
}

func (mt *multitracker) statefulsetFailed(spec MultitrackSpec, feed statefulset.Feed, reason string) error {
	mt.displayResourceErrorF("sts", spec.ResourceName, "%s\n", reason)

	return mt.handleResourceFailure(mt.TrackingStatefulSets, "sts", spec, reason)
}

func (mt *multitracker) statefulsetEventMsg(spec MultitrackSpec, feed statefulset.Feed, msg string) error {
	mt.displayResourceTrackerMessageF("sts", spec.ResourceName, "%s\n", msg)
	return nil
}

func (mt *multitracker) statefulsetAddedReplicaSet(spec MultitrackSpec, feed statefulset.Feed, rs replicaset.ReplicaSet) error {
	if !rs.IsNew {
		return nil
	}

	mt.displayResourceTrackerMessageF("sts", spec.ResourceName, "rs/%s added\n", rs.Name)

	return nil
}

func (mt *multitracker) statefulsetAddedPod(spec MultitrackSpec, feed statefulset.Feed, pod replicaset.ReplicaSetPod) error {
	if !pod.ReplicaSet.IsNew {
		return nil
	}

	mt.displayResourceTrackerMessageF("sts", spec.ResourceName, "po/%s added\n", pod.Name)

	return nil
}

func (mt *multitracker) statefulsetPodError(spec MultitrackSpec, feed statefulset.Feed, podError replicaset.ReplicaSetPodError) error {
	if !podError.ReplicaSet.IsNew {
		return nil
	}

	reason := fmt.Sprintf("po/%s container/%s: %s", podError.PodName, podError.ContainerName, podError.Message)

	mt.displayResourceErrorF("sts", spec.ResourceName, "%s\n", reason)

	return mt.handleResourceFailure(mt.TrackingStatefulSets, "sts", spec, reason)
}

func (mt *multitracker) statefulsetPodLogChunk(spec MultitrackSpec, feed statefulset.Feed, chunk *replicaset.ReplicaSetPodLogChunk) error {
	if !chunk.ReplicaSet.IsNew {
		return nil
	}

	mt.displayResourceLogChunk("sts", spec.ResourceName, podContainerLogChunkHeader(chunk.PodName, chunk.ContainerLogChunk), spec, chunk.ContainerLogChunk)

	return nil
}

func (mt *multitracker) statefulsetStatusReport(spec MultitrackSpec, feed statefulset.Feed, status statefulset.StatefulSetStatus) error {
	mt.StatefulSetsStatuses[spec.ResourceName] = status
	return nil
}
