package canary

import (
	v1beta1 "github.com/fluxcd/flagger/pkg/apis/flagger/v1beta1"
	"github.com/werf/kubedog/pkg/tracker/indicators"
	"github.com/werf/kubedog/pkg/utils"
)

type CanaryStatus struct {
	v1beta1.CanaryStatus

	StatusGeneration uint64

	StatusIndicator *indicators.StringEqualConditionIndicator

	Duration string
	Age      string

	IsSucceeded  bool
	IsFailed     bool
	FailedReason string
}

func NewCanaryStatus(object *v1beta1.Canary, statusGeneration uint64, isTrackerFailed bool, trackerFailedReason string, canariesStatuses map[string]v1beta1.CanaryStatus) CanaryStatus {
	res := CanaryStatus{
		CanaryStatus:     object.Status,
		StatusGeneration: statusGeneration,
		StatusIndicator:  &indicators.StringEqualConditionIndicator{},
		Age:              utils.TranslateTimestampSince(object.CreationTimestamp),
	}

	switch object.Status.Phase {
	case v1beta1.CanaryPhaseInitialized, v1beta1.CanaryPhaseSucceeded:
		res.IsSucceeded = true
	case v1beta1.CanaryPhaseFailed:
		if !res.IsFailed {
			errorMessage := "Failed - "
			for _, condition := range object.Status.Conditions {
				errorMessage += condition.Message
			}
			res.IsFailed = true
			res.FailedReason = errorMessage
		}
	default:
		res.StatusIndicator.Value = string(object.Status.Phase)
	}

	if !res.IsSucceeded && !res.IsFailed {
		res.IsFailed = isTrackerFailed
		res.FailedReason = trackerFailedReason
	}

	return res
}
