package multitrack

import (
	"fmt"

	"github.com/flant/kubedog/pkg/display"
	"github.com/flant/kubedog/pkg/tracker/pod"
	"k8s.io/client-go/kubernetes"
)

func (mt *multitracker) TrackPod(kube kubernetes.Interface, spec MultitrackSpec, opts MultitrackOptions) error {
	feed := pod.NewFeed()

	feed.OnAdded(func() error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.podAdded(spec, feed)
	})
	feed.OnSucceeded(func() error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.podSucceeded(spec, feed)
	})
	feed.OnFailed(func(reason string) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.podFailed(spec, feed, reason)
	})
	feed.OnReady(func() error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.podReady(spec, feed)
	})
	feed.OnEventMsg(func(msg string) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.podEventMsg(spec, feed, msg)
	})
	feed.OnContainerError(func(containerError pod.ContainerError) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.podContainerError(spec, feed, containerError)
	})
	feed.OnContainerLogChunk(func(chunk *pod.ContainerLogChunk) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.podContainerLogChunk(spec, feed, chunk)
	})
	feed.OnStatusReport(func(status pod.PodStatus) error {
		mt.handlerMux.Lock()
		defer mt.handlerMux.Unlock()
		return mt.podStatusReport(spec, feed, status)
	})

	return feed.Track(spec.ResourceName, spec.Namespace, kube, opts.Options)
}

func (mt *multitracker) podAdded(spec MultitrackSpec, feed pod.Feed) error {
	if debug() {
		fmt.Printf("-- podAdded %#v\n", spec)
	}

	display.OutF("# po/%s added\n", spec.ResourceName)

	return nil
}

func (mt *multitracker) podSucceeded(spec MultitrackSpec, feed pod.Feed) error {
	if debug() {
		fmt.Printf("-- podSucceeded %#v\n", spec)
	}

	mt.PodsStatuses[spec.ResourceName] = feed.GetStatus()

	display.OutF("# po/%s succeeded\n", spec.ResourceName)

	return mt.handleResourceReadyCondition(mt.TrackingPods, spec)
}

func (mt *multitracker) podFailed(spec MultitrackSpec, feed pod.Feed, reason string) error {
	if debug() {
		fmt.Printf("-- podFailed %#v %#v\n", spec, reason)
	}

	fmt.Fprintf(display.Out, "# po/%s failed: %s\n", spec.ResourceName, reason)

	return mt.handleResourceFailure(mt.TrackingPods, spec, reason)
}

func (mt *multitracker) podReady(spec MultitrackSpec, feed pod.Feed) error {
	if debug() {
		fmt.Printf("-- podReady %#v\n", spec)
	}

	mt.PodsStatuses[spec.ResourceName] = feed.GetStatus()

	display.OutF("# po/%s become READY\n", spec.ResourceName)

	delete(mt.TrackingPods, spec.ResourceName)

	return mt.handleResourceReadyCondition(mt.TrackingPods, spec)
}

func (mt *multitracker) podEventMsg(spec MultitrackSpec, feed pod.Feed, msg string) error {
	if debug() {
		fmt.Printf("-- podEventMsg %#v %#v\n", spec, msg)
	}

	display.OutF("# po/%s event: %s\n", spec.ResourceName, msg)

	return nil
}

func (mt *multitracker) podContainerError(spec MultitrackSpec, feed pod.Feed, containerError pod.ContainerError) error {
	if debug() {
		fmt.Printf("-- podContainerError %#v %#v\n", spec, containerError)
	}

	reason := fmt.Sprintf("container/%s error: %s", containerError.ContainerName, containerError.Message)

	display.OutF("# po/%s %s\n", spec.ResourceName, reason)

	return mt.handleResourceFailure(mt.TrackingPods, spec, reason)
}

func (mt *multitracker) podContainerLogChunk(spec MultitrackSpec, feed pod.Feed, chunk *pod.ContainerLogChunk) error {
	if debug() {
		fmt.Printf("-- podContainerLogChunk %#v %#v\n", spec, chunk)
	}

	header := fmt.Sprintf("po/%s %s", spec.ResourceName, chunk.ContainerName)
	display.OutputLogLines(header, chunk.LogLines)

	return nil
}

func (mt *multitracker) podStatusReport(spec MultitrackSpec, feed pod.Feed, status pod.PodStatus) error {
	if debug() {
		fmt.Printf("-- podStatusReport %#v %#v\n", spec, status)
	}

	mt.PodsStatuses[spec.ResourceName] = status

	return nil
}
