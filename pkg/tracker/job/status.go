package job

import (
	"fmt"
	"time"

	"github.com/flant/kubedog/pkg/utils"

	"github.com/flant/kubedog/pkg/tracker/indicators"
	"github.com/flant/kubedog/pkg/tracker/pod"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/duration"
)

type JobStatus struct {
	batchv1.JobStatus

	StatusGeneration uint64

	SucceededIndicator *indicators.Int32EqualConditionIndicator
	Duration           string
	Age                string

	WaitingForMessages []string

	IsComplete   bool
	IsFailed     bool
	FailedReason string

	Pods map[string]pod.PodStatus
}

func NewJobStatus(object *batchv1.Job, statusGeneration uint64, isFailed bool, failedReason string, podsStatuses map[string]pod.PodStatus, trackedPodsNames []string) JobStatus {
	res := JobStatus{
		JobStatus:        object.Status,
		StatusGeneration: statusGeneration,
		Age:              utils.TranslateTimestampSince(object.CreationTimestamp),
		IsFailed:         isFailed,
		FailedReason:     failedReason,
		Pods:             make(map[string]pod.PodStatus),
	}

	for k, v := range podsStatuses {
		res.Pods[k] = v
		if v.StatusIndicator != nil {
			v.StatusIndicator.TargetValue = "Completed"
		}
	}

	switch {
	case res.StartTime == nil:
	case res.CompletionTime == nil:
		res.Duration = duration.HumanDuration(time.Since(res.StartTime.Time))
	default:
		res.Duration = duration.HumanDuration(res.CompletionTime.Sub(res.StartTime.Time))
	}

	if len(trackedPodsNames) == 0 {
		for _, c := range object.Status.Conditions {
			if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
				res.IsComplete = true
			} else {
				res.WaitingForMessages = append(res.WaitingForMessages, fmt.Sprintf("condition %s->%s", batchv1.JobComplete, corev1.ConditionTrue))

				if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
					if !res.IsFailed {
						res.IsFailed = true
						res.FailedReason = c.Reason
					}
				}
			}
		}
	} else {
		res.WaitingForMessages = append(res.WaitingForMessages, "pods should be complete")
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

		if !res.IsComplete {
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

	return res
}
