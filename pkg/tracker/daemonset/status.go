package daemonset

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"

	"github.com/werf/kubedog/pkg/tracker/indicators"
	"github.com/werf/kubedog/pkg/tracker/pod"
)

type DaemonSetStatus struct {
	appsv1.DaemonSetStatus

	StatusGeneration uint64

	ReplicasIndicator  *indicators.Int32EqualConditionIndicator
	UpToDateIndicator  *indicators.Int32EqualConditionIndicator
	AvailableIndicator *indicators.Int32EqualConditionIndicator

	WaitingForMessages []string

	IsReady      bool
	IsFailed     bool
	FailedReason string

	Pods         map[string]pod.PodStatus
	NewPodsNames []string
}

func NewDaemonSetStatus(object *appsv1.DaemonSet, statusGeneration uint64, isTrackerFailed bool, trackerFailedReason string, podsStatuses map[string]pod.PodStatus, newPodsNames []string) DaemonSetStatus {
	res := DaemonSetStatus{
		StatusGeneration: statusGeneration,
		DaemonSetStatus:  object.Status,
		Pods:             make(map[string]pod.PodStatus),
		NewPodsNames:     newPodsNames,
	}

processingPodsStatuses:
	for k, v := range podsStatuses {
		res.Pods[k] = v

		for _, newPodName := range newPodsNames {
			if newPodName == k {
				if v.StatusIndicator != nil {
					// New Pod should be Running
					v.StatusIndicator.TargetValue = "Running"
				}
				continue processingPodsStatuses
			}
		}

		if v.StatusIndicator != nil {
			// Old Pod should gone
			v.StatusIndicator.TargetValue = ""
		}
	}

	res.IsReady = false

	// FIXME: tracker should track other update strategy types as well
	if object.Spec.UpdateStrategy.Type != appsv1.RollingUpdateDaemonSetStrategyType {
		res.IsReady = true
		return res
	}

	if object.Status.ObservedGeneration >= object.Generation {
		res.ReplicasIndicator = &indicators.Int32EqualConditionIndicator{
			Value:       object.Status.CurrentNumberScheduled + object.Status.NumberMisscheduled,
			TargetValue: object.Status.DesiredNumberScheduled,
		}
		res.UpToDateIndicator = &indicators.Int32EqualConditionIndicator{
			Value:       object.Status.UpdatedNumberScheduled,
			TargetValue: object.Status.DesiredNumberScheduled,
		}
		res.AvailableIndicator = &indicators.Int32EqualConditionIndicator{
			Value:       object.Status.NumberAvailable,
			TargetValue: object.Status.DesiredNumberScheduled,
		}

		res.IsReady = true

		if object.Status.UpdatedNumberScheduled != object.Status.DesiredNumberScheduled {
			res.IsReady = false
			res.WaitingForMessages = append(res.WaitingForMessages, fmt.Sprintf("up-to-date %d->%d", object.Status.UpdatedNumberScheduled, object.Status.DesiredNumberScheduled))
		}
		if object.Status.NumberAvailable != object.Status.DesiredNumberScheduled {
			res.IsReady = false
			res.WaitingForMessages = append(res.WaitingForMessages, fmt.Sprintf("available %d->%d", object.Status.NumberAvailable, object.Status.DesiredNumberScheduled))
		}
	} else {
		res.WaitingForMessages = append(res.WaitingForMessages, fmt.Sprintf("observed generation %d should be >= %d", object.Status.ObservedGeneration, object.Generation))
	}

	if !res.IsReady && !res.IsFailed {
		res.IsFailed = isTrackerFailed
		res.FailedReason = trackerFailedReason
	}

	return res
}

