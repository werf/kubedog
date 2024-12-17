package multitrack

import (
	"k8s.io/client-go/kubernetes"

	"github.com/werf/kubedog/for-werf-helm/pkg/tracker/canary"
)

func (mt *multitracker) TrackCanary(kube kubernetes.Interface, spec MultitrackSpec, opts MultitrackOptions) error {
	feed := canary.NewFeed()

	feed.OnAdded(func() error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.CanariesStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.canaryAdded(spec, feed)
	})
	feed.OnSucceeded(func() error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.CanariesStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.canarySucceeded(spec, feed)
	})
	feed.OnFailed(func(reason string) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.CanariesStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.canaryFailed(spec, feed, reason)
	})
	feed.OnEventMsg(func(msg string) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.CanariesStatuses[spec.ResourceName] = feed.GetStatus()

		return mt.canaryEventMsg(spec, feed, msg)
	})

	feed.OnStatus(func(status canary.CanaryStatus) error {
		mt.mux.Lock()
		defer mt.mux.Unlock()

		mt.CanariesStatuses[spec.ResourceName] = status

		return nil
	})

	return feed.Track(spec.ResourceName, spec.Namespace, kube, opts.Options)
}

func (mt *multitracker) canaryAdded(spec MultitrackSpec, feed canary.Feed) error {
	mt.displayResourceTrackerMessageF("canary", spec.ResourceName, spec.ShowServiceMessages, "added")

	return nil
}

func (mt *multitracker) canarySucceeded(spec MultitrackSpec, feed canary.Feed) error {
	mt.displayResourceTrackerMessageF("canary", spec.ResourceName, spec.ShowServiceMessages, "succeeded")

	return mt.handleResourceReadyCondition(mt.TrackingCanaries, spec)
}

func (mt *multitracker) canaryFailed(spec MultitrackSpec, feed canary.Feed, reason string) error {
	mt.displayResourceErrorF("canary", spec.ResourceName, "%s", reason)

	return mt.handleResourceFailure(mt.TrackingCanaries, "canary", spec, reason)
}

func (mt *multitracker) canaryEventMsg(spec MultitrackSpec, feed canary.Feed, msg string) error {
	mt.displayResourceEventF("canary", spec.ResourceName, spec.ShowServiceMessages, "%s", msg)
	return nil
}
