package job

import (
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/werf/kubedog-for-werf-helm/pkg/tracker/indicators"
	"github.com/werf/kubedog-for-werf-helm/pkg/tracker/pod"
	"github.com/werf/kubedog-for-werf-helm/pkg/utils"
)

type JobStatus struct {
	batchv1.JobStatus

	StatusGeneration uint64

	SucceededIndicator *indicators.Int32EqualConditionIndicator
	Age                string

	WaitingForMessages []string

	IsSucceeded  bool
	IsFailed     bool
	FailedReason string

	Pods map[string]pod.PodStatus
}

func NewJobStatus(object *batchv1.Job, statusGeneration uint64, isTrackerFailed bool, trackerFailedReason string, podsStatuses map[string]pod.PodStatus, trackedPodsNames []string) JobStatus {
	res := JobStatus{
		JobStatus:        object.Status,
		StatusGeneration: statusGeneration,
		Age:              utils.TranslateTimestampSince(object.CreationTimestamp),
		Pods:             make(map[string]pod.PodStatus),
	}

	for k, v := range podsStatuses {
		res.Pods[k] = v
		if v.StatusIndicator != nil {
			v.StatusIndicator.TargetValue = "Completed"
		}
	}

	doCheckJobConditions := true
	for _, trackedPodName := range trackedPodsNames {
		podStatus := podsStatuses[trackedPodName]

		if !podStatus.IsFailed && !podStatus.IsSucceeded {
			doCheckJobConditions = false // unterminated pods exists
		}
	}

	if doCheckJobConditions {
		for _, c := range object.Status.Conditions {
			switch c.Type {
			case batchv1.JobComplete:
				if c.Status == corev1.ConditionTrue {
					res.IsSucceeded = true
				}

			case batchv1.JobFailed:
				if c.Status == corev1.ConditionTrue {
					if !res.IsFailed {
						res.IsFailed = true
						res.FailedReason = c.Reason
					}
				}
			}
		}

		if !res.IsSucceeded {
			res.WaitingForMessages = append(res.WaitingForMessages, fmt.Sprintf("condition %s->%s", batchv1.JobComplete, corev1.ConditionTrue))
		}
	} else {
		res.WaitingForMessages = append(res.WaitingForMessages, "pods should be terminated")
	}

	res.SucceededIndicator = &indicators.Int32EqualConditionIndicator{}
	res.SucceededIndicator.Value = object.Status.Succeeded

	if object.Spec.Completions != nil {
		res.SucceededIndicator.TargetValue = *object.Spec.Completions

		if !res.SucceededIndicator.IsReady() {
			res.WaitingForMessages = append(res.WaitingForMessages, fmt.Sprintf("succeeded %d->%d", res.SucceededIndicator.Value, res.SucceededIndicator.TargetValue))
		}
	} else {
		res.SucceededIndicator.TargetValue = 1

		if !res.IsSucceeded {
			parallelism := int32(0)
			if object.Spec.Parallelism != nil {
				parallelism = *object.Spec.Parallelism
			}
			if parallelism > 1 {
				res.WaitingForMessages = append(res.WaitingForMessages, fmt.Sprintf("succeeded %d->%d of %d", res.SucceededIndicator.Value, res.SucceededIndicator.TargetValue, parallelism))
			} else {
				res.WaitingForMessages = append(res.WaitingForMessages, fmt.Sprintf("succeeded %d->%d", res.SucceededIndicator.Value, res.SucceededIndicator.TargetValue))
			}
		}
	}

	if !res.IsSucceeded && !res.IsFailed {
		res.IsFailed = isTrackerFailed
		res.FailedReason = trackerFailedReason
	}

	return res
}
