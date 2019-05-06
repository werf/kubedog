package multitrack

import (
	"fmt"

	"github.com/flant/kubedog/pkg/display"
	"github.com/flant/kubedog/pkg/tracker/daemonset"
	"github.com/flant/kubedog/pkg/tracker/replicaset"
	"k8s.io/client-go/kubernetes"
)

func (mt *multitracker) TrackDaemonSet(kube kubernetes.Interface, spec MultitrackSpec, opts MultitrackOptions) error {
	feed := daemonset.NewFeed()

	feed.OnAdded(func(ready bool) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.daemonsetAdded(spec, feed, ready)
	})
	feed.OnReady(func() error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.daemonsetReady(spec, feed)
	})
	feed.OnFailed(func(reason string) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.daemonsetFailed(spec, feed, reason)
	})
	feed.OnEventMsg(func(msg string) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.daemonsetEventMsg(spec, feed, msg)
	})
	feed.OnAddedReplicaSet(func(rs replicaset.ReplicaSet) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.daemonsetAddedReplicaSet(spec, feed, rs)
	})
	feed.OnAddedPod(func(pod replicaset.ReplicaSetPod) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.daemonsetAddedPod(spec, feed, pod)
	})
	feed.OnPodError(func(podError replicaset.ReplicaSetPodError) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.daemonsetPodError(spec, feed, podError)
	})
	feed.OnPodLogChunk(func(chunk *replicaset.ReplicaSetPodLogChunk) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.daemonsetPodLogChunk(spec, feed, chunk)
	})
	feed.OnStatusReport(func(status daemonset.DaemonSetStatus) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.daemonsetStatusReport(spec, feed, status)
	})

	return feed.Track(spec.ResourceName, spec.Namespace, kube, opts.Options)
}

func (mt *multitracker) daemonsetAdded(spec MultitrackSpec, feed daemonset.Feed, ready bool) error {
	if debug() {
		fmt.Printf("-- daemonsetAdded %#v %#v\n", spec, ready)
	}

	if ready {
		mt.DaemonSetsStatuses[spec.ResourceName] = feed.GetStatus()

		display.OutF("# ds/%s appears to be READY\n", spec.ResourceName)

		return mt.handleResourceReadyCondition(mt.TrackingDaemonSets, spec)
	}

	display.OutF("# ds/%s added\n", spec.ResourceName)

	return nil
}

func (mt *multitracker) daemonsetReady(spec MultitrackSpec, feed daemonset.Feed) error {
	if debug() {
		fmt.Printf("-- daemonsetReady %#v\n", spec)
	}

	mt.DaemonSetsStatuses[spec.ResourceName] = feed.GetStatus()

	display.OutF("# ds/%s become READY\n", spec.ResourceName)

	return mt.handleResourceReadyCondition(mt.TrackingDaemonSets, spec)
}

func (mt *multitracker) daemonsetFailed(spec MultitrackSpec, feed daemonset.Feed, reason string) error {
	if debug() {
		fmt.Printf("-- daemonsetFailed %#v %#v\n", spec, reason)
	}

	display.OutF("# ds/%s FAIL: %s\n", spec.ResourceName, reason)

	return mt.handleResourceFailure(mt.TrackingDaemonSets, spec, reason)
}

func (mt *multitracker) daemonsetEventMsg(spec MultitrackSpec, feed daemonset.Feed, msg string) error {
	if debug() {
		fmt.Printf("-- daemonsetEventMsg %#v %#v\n", spec, msg)
	}

	display.OutF("# ds/%s event: %s\n", spec.ResourceName, msg)

	return nil
}

func (mt *multitracker) daemonsetAddedReplicaSet(spec MultitrackSpec, feed daemonset.Feed, rs replicaset.ReplicaSet) error {
	if debug() {
		fmt.Printf("-- daemonsetAddedReplicaSet %#v %#v\n", spec, rs)
	}

	if !rs.IsNew {
		return nil
	}
	display.OutF("# ds/%s rs/%s added\n", spec.ResourceName, rs.Name)

	return nil
}

func (mt *multitracker) daemonsetAddedPod(spec MultitrackSpec, feed daemonset.Feed, pod replicaset.ReplicaSetPod) error {
	if debug() {
		fmt.Printf("-- daemonsetAddedPod %#v %#v\n", spec, pod)
	}

	if !pod.ReplicaSet.IsNew {
		return nil
	}
	display.OutF("# ds/%s po/%s added\n", spec.ResourceName, pod.Name)

	return nil
}

func (mt *multitracker) daemonsetPodError(spec MultitrackSpec, feed daemonset.Feed, podError replicaset.ReplicaSetPodError) error {
	if debug() {
		fmt.Printf("-- daemonsetPodError %#v %#v\n", spec, podError)
	}

	if !podError.ReplicaSet.IsNew {
		return nil
	}

	reason := fmt.Sprintf("po/%s %s error: %s", podError.PodName, podError.ContainerName, podError.Message)

	display.OutF("# ds/%s %s\n", spec.ResourceName, reason)

	return mt.handleResourceFailure(mt.TrackingDaemonSets, spec, reason)
}

func (mt *multitracker) daemonsetPodLogChunk(spec MultitrackSpec, feed daemonset.Feed, chunk *replicaset.ReplicaSetPodLogChunk) error {
	if debug() {
		fmt.Printf("-- daemonsetPodLogChunk %#v %#v\n", spec, chunk)
	}

	if !chunk.ReplicaSet.IsNew {
		return nil
	}

	header := fmt.Sprintf("ds/%s %s", spec.ResourceName, podContainerLogChunkHeader(chunk.PodName, chunk.ContainerLogChunk))
	displayContainerLogChunk(header, spec, chunk.ContainerLogChunk)

	return nil
}

func (mt *multitracker) daemonsetStatusReport(spec MultitrackSpec, feed daemonset.Feed, status daemonset.DaemonSetStatus) error {
	if debug() {
		fmt.Printf("-- daemonsetStatusReport %#v %#v\n", spec, status)
	}

	mt.DaemonSetsStatuses[spec.ResourceName] = status

	return nil
}
