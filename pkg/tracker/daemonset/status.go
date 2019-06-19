package daemonset

import (
	"fmt"

	"github.com/flant/kubedog/pkg/tracker/indicators"
	"github.com/flant/kubedog/pkg/tracker/pod"
	extensions "k8s.io/api/extensions/v1beta1"
)

type DaemonSetStatus struct {
	extensions.DaemonSetStatus

	StatusGeneration uint64

	ReadyReplicasIndicator     *indicators.Int32EqualConditionIndicator
	UpdatedReplicasIndicator   *indicators.Int32EqualConditionIndicator
	AvailableReplicasIndicator *indicators.Int32EqualConditionIndicator
	OldReplicasIndicator       *indicators.Int32EqualConditionIndicator

	IsReady      bool
	IsFailed     bool
	FailedReason string

	Pods map[string]pod.PodStatus
}

func NewDaemonSetStatus(object *extensions.DaemonSet, statusGeneration uint64, isFailed bool, failedReason string, podsStatuses map[string]pod.PodStatus) DaemonSetStatus {
	res := DaemonSetStatus{
		DaemonSetStatus: object.Status,
		Pods:            make(map[string]pod.PodStatus),
	}
	for k, v := range podsStatuses {
		res.Pods[k] = v
	}

	res.IsReady = false

	// FIXME: tracker should track other update strategy types as well
	if object.Spec.UpdateStrategy.Type != extensions.RollingUpdateDaemonSetStrategyType {
		res.IsReady = true
		return res
	}

	if object.Generation <= object.Status.ObservedGeneration {
		res.ReadyReplicasIndicator = &indicators.Int32EqualConditionIndicator{
			Value:       object.Status.NumberReady,
			TargetValue: object.Status.DesiredNumberScheduled,
		}
		res.UpdatedReplicasIndicator = &indicators.Int32EqualConditionIndicator{
			Value:       object.Status.UpdatedNumberScheduled,
			TargetValue: object.Status.DesiredNumberScheduled,
		}
		res.AvailableReplicasIndicator = &indicators.Int32EqualConditionIndicator{
			Value:       object.Status.NumberAvailable,
			TargetValue: object.Status.DesiredNumberScheduled,
		}
		//res.OldReplicasIndicator = &indicators.Int32EqualConditionIndicator{
		//	Value:       object.Status.DesiredNumberScheduled - object.Status.UpdatedNumberScheduled,
		//	TargetValue: 0,
		//}

		if object.Status.UpdatedNumberScheduled == object.Status.DesiredNumberScheduled && object.Status.NumberAvailable == object.Status.DesiredNumberScheduled {
			res.IsReady = true
			return res
		}
	}

	return res
}

// Status returns a message describing daemon set status, and a bool value indicating if the status is considered done.
func DaemonSetRolloutStatus(daemon *extensions.DaemonSet) (string, bool, error) {
	if daemon.Spec.UpdateStrategy.Type != extensions.RollingUpdateDaemonSetStrategyType {
		return "", true, fmt.Errorf("rollout status is only available for %s strategy type", extensions.RollingUpdateDaemonSetStrategyType)
	}
	if daemon.Generation <= daemon.Status.ObservedGeneration {
		if daemon.Status.UpdatedNumberScheduled < daemon.Status.DesiredNumberScheduled {
			return fmt.Sprintf("Waiting for daemon set %q rollout to finish: %d out of %d new pods have been updated...\n", daemon.Name, daemon.Status.UpdatedNumberScheduled, daemon.Status.DesiredNumberScheduled), false, nil
		}
		if daemon.Status.NumberAvailable < daemon.Status.DesiredNumberScheduled {
			return fmt.Sprintf("Waiting for daemon set %q rollout to finish: %d of %d updated pods are available...\n", daemon.Name, daemon.Status.NumberAvailable, daemon.Status.DesiredNumberScheduled), false, nil
		}
		return fmt.Sprintf("daemon set %q successfully rolled out\n", daemon.Name), true, nil
	}
	return fmt.Sprintf("Waiting for daemon set spec update to be observed...\n"), false, nil
}
