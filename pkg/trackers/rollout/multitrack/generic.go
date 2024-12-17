package multitrack

import (
	"time"

	corev1 "k8s.io/api/core/v1"

	gentrck "github.com/werf/kubedog-for-werf-helm/pkg/tracker/generic"
	"github.com/werf/kubedog-for-werf-helm/pkg/trackers/rollout/multitrack/generic"
)

func (mt *multitracker) TrackGeneric(resource *generic.Resource, timeout, noActivityTimeout time.Duration) error {
	resource.Feed.OnAdded(func(status *gentrck.ResourceStatus) error {
		return mt.genericAdded(resource, status)
	})

	resource.Feed.OnReady(func(status *gentrck.ResourceStatus) error {
		return mt.genericReady(resource, status)
	})

	resource.Feed.OnFailed(func(status *gentrck.ResourceStatus) error {
		return mt.genericFailed(resource, status)
	})

	resource.Feed.OnStatus(func(status *gentrck.ResourceStatus) error {
		resource.State.SetLastStatus(status)
		return nil
	})

	resource.Feed.OnEventMsg(func(event *corev1.Event) error {
		return mt.genericEventMsg(resource, event)
	})

	return resource.Feed.Track(resource.Context.Context(), timeout, noActivityTimeout)
}

func (mt *multitracker) genericAdded(resource *generic.Resource, status *gentrck.ResourceStatus) error {
	resource.State.SetLastStatus(status)

	mt.mux.Lock()
	defer mt.mux.Unlock()

	if status.IsReady() {
		mt.displayResourceTrackerMessageF(resource.Spec.GroupVersionKindNamespaceString(), resource.Spec.Name, resource.Spec.ShowServiceMessages, "appears to be READY")
		return mt.handleGenericResourceReadyCondition(resource)
	}

	mt.displayResourceTrackerMessageF(resource.Spec.GroupVersionKindNamespaceString(), resource.Spec.Name, resource.Spec.ShowServiceMessages, "added")

	return nil
}

func (mt *multitracker) genericReady(resource *generic.Resource, status *gentrck.ResourceStatus) error {
	resource.State.SetLastStatus(status)

	mt.mux.Lock()
	defer mt.mux.Unlock()

	mt.displayResourceTrackerMessageF(resource.Spec.GroupVersionKindNamespaceString(), resource.Spec.Name, resource.Spec.ShowServiceMessages, "become READY")

	return mt.handleGenericResourceReadyCondition(resource)
}

func (mt *multitracker) genericFailed(resource *generic.Resource, status *gentrck.ResourceStatus) error {
	resource.State.SetLastStatus(status)

	mt.mux.Lock()
	defer mt.mux.Unlock()

	mt.displayResourceErrorF(resource.Spec.GroupVersionKindNamespaceString(), resource.Spec.Name, "%s", status.FailureReason())

	return mt.handleGenericResourceFailure(resource, status.FailureReason())
}

func (mt *multitracker) genericEventMsg(resource *generic.Resource, event *corev1.Event) error {
	mt.mux.Lock()
	defer mt.mux.Unlock()

	mt.displayResourceEventF(resource.Spec.GroupVersionKindNamespaceString(), resource.Spec.Name, !resource.Spec.HideEvents, "%s: %s", event.Reason, event.Message)

	return nil
}
