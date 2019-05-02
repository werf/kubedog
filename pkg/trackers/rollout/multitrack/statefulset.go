package multitrack

import (
	"fmt"

	"github.com/flant/kubedog/pkg/display"
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
	if debug() {
		fmt.Printf("-- statefulsetAdded %#v %#v\n", spec, ready)
	}

	if ready {
		mt.StatefulSetsStatuses[spec.ResourceName] = feed.GetStatus()

		display.OutF("# sts/%s appears to be READY\n", spec.ResourceName)

		return mt.handleResourceReadyCondition(mt.TrackingStatefulSets, spec)
	}

	display.OutF("# sts/%s added\n", spec.ResourceName)

	return nil
}

func (mt *multitracker) statefulsetReady(spec MultitrackSpec, feed statefulset.Feed) error {
	if debug() {
		fmt.Printf("-- statefulsetReady %#v\n", spec)
	}

	mt.StatefulSetsStatuses[spec.ResourceName] = feed.GetStatus()

	display.OutF("# sts/%s become READY\n", spec.ResourceName)

	return mt.handleResourceReadyCondition(mt.TrackingStatefulSets, spec)
}

func (mt *multitracker) statefulsetFailed(spec MultitrackSpec, feed statefulset.Feed, reason string) error {
	if debug() {
		fmt.Printf("-- statefulsetFailed %#v %#v\n", spec, reason)
	}

	display.OutF("# sts/%s FAIL: %s\n", spec.ResourceName, reason)

	return mt.handleResourceFailure(mt.TrackingStatefulSets, spec, reason)
}

func (mt *multitracker) statefulsetEventMsg(spec MultitrackSpec, feed statefulset.Feed, msg string) error {
	if debug() {
		fmt.Printf("-- statefulsetEventMsg %#v %#v\n", spec, msg)
	}

	display.OutF("# sts/%s event: %s\n", spec.ResourceName, msg)

	return nil
}

func (mt *multitracker) statefulsetAddedReplicaSet(spec MultitrackSpec, feed statefulset.Feed, rs replicaset.ReplicaSet) error {
	if debug() {
		fmt.Printf("-- statefulsetAddedReplicaSet %#v %#v\n", spec, rs)
	}

	if !rs.IsNew {
		return nil
	}
	display.OutF("# sts/%s rs/%s added\n", spec.ResourceName, rs.Name)

	return nil
}

func (mt *multitracker) statefulsetAddedPod(spec MultitrackSpec, feed statefulset.Feed, pod replicaset.ReplicaSetPod) error {
	if debug() {
		fmt.Printf("-- statefulsetAddedPod %#v %#v\n", spec, pod)
	}

	if !pod.ReplicaSet.IsNew {
		return nil
	}
	display.OutF("# sts/%s po/%s added\n", spec.ResourceName, pod.Name)

	return nil
}

func (mt *multitracker) statefulsetPodError(spec MultitrackSpec, feed statefulset.Feed, podError replicaset.ReplicaSetPodError) error {
	if debug() {
		fmt.Printf("-- statefulsetPodError %#v %#v\n", spec, podError)
	}

	if !podError.ReplicaSet.IsNew {
		return nil
	}

	reason := fmt.Sprintf("po/%s %s error: %s", podError.PodName, podError.ContainerName, podError.Message)

	display.OutF("# sts/%s %s\n", spec.ResourceName, reason)

	return mt.handleResourceFailure(mt.TrackingStatefulSets, spec, reason)
}

func (mt *multitracker) statefulsetPodLogChunk(spec MultitrackSpec, feed statefulset.Feed, chunk *replicaset.ReplicaSetPodLogChunk) error {
	if debug() {
		fmt.Printf("-- statefulsetPodLogChunk %#v %#v\n", spec, chunk)
	}

	if !chunk.ReplicaSet.IsNew {
		return nil
	}

	header := fmt.Sprintf("sts/%s %s", spec.ResourceName, podContainerLogChunkHeader(chunk.PodName, chunk.ContainerLogChunk))
	displayContainerLogChunk(header, spec, chunk.ContainerLogChunk)

	return nil
}

func (mt *multitracker) statefulsetStatusReport(spec MultitrackSpec, feed statefulset.Feed, status statefulset.StatefulSetStatus) error {
	if debug() {
		fmt.Printf("-- statefulsetStatusReport %#v %#v\n", spec, status)
	}

	mt.StatefulSetsStatuses[spec.ResourceName] = status

	return nil
}
