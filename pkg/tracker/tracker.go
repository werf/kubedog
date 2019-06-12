package tracker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fatih/color"
	"k8s.io/client-go/kubernetes"
)

var (
	ErrTrackInterrupted = errors.New("tracker interrupted")
	StopTrack           = errors.New("stop tracking now")
)

const (
	Initial                TrackerState = "Initial"
	ResourceAdded          TrackerState = "ResourceAdded"
	ResourceSucceeded      TrackerState = "ResourceSucceeded"
	ResourceFailed         TrackerState = "ResourceFailed"
	FollowingContainerLogs TrackerState = "FollowingContainerLogs"
	ContainerTrackerDone   TrackerState = "ContainerTrackerDone"
)

type TrackerState string

type Tracker struct {
	Kube             kubernetes.Interface
	Namespace        string
	ResourceName     string
	FullResourceName string // full resource name with resource kind (deploy/superapp)
	Context          context.Context
	ContextCancel    context.CancelFunc
}

type Options struct {
	ParentContext context.Context
	Timeout       time.Duration
	LogsFromTime  time.Time
}

type ResourceError struct {
	msg string
}

func (r *ResourceError) Error() string {
	return r.msg
}

func ResourceErrorf(format string, a ...interface{}) error {
	return &ResourceError{
		msg: fmt.Sprintf(format, a...),
	}
}

type StringEqualConditionIndicator struct {
	Value                     string
	PrevValue                 string
	TargetValue               string
	IsReadyConditionSatisfied bool
	IsProgressing             bool
}

func (indicator StringEqualConditionIndicator) FormatTableElem(noColor bool) string {
	if indicator.IsReadyConditionSatisfied {
		if noColor {
			return indicator.Value
		}

		return color.New(color.FgGreen).Sprintf("%s", indicator.Value)
	} else {
		if noColor {
			return indicator.Value
		}

		return color.New(color.FgYellow).Sprintf("%s", indicator.Value)
	}
}

type Int64GreaterOrEqualConditionIndicator struct {
	Value                     int64
	PrevValue                 int64
	TargetValue               int64
	IsReadyConditionSatisfied bool
	IsProgressing             bool
}

type Int32EqualConditionIndicator struct {
	Value                     int32
	PrevValue                 int32
	TargetValue               int32
	IsReadyConditionSatisfied bool
	IsProgressing             bool
}

func (indicator Int32EqualConditionIndicator) FormatTableElem(noColor bool) string {
	res := ""

	if indicator.IsReadyConditionSatisfied {
		if noColor {
			res = fmt.Sprintf("%d", indicator.Value)
		} else {
			res = color.New(color.FgGreen).Sprintf("%d", indicator.Value)
		}
	} else {
		if noColor {
			res = fmt.Sprintf("%d", indicator.Value)
		} else {
			res = color.New(color.FgYellow).Sprintf("%d", indicator.Value)
		}

		if indicator.IsProgressing {
			progressValue := indicator.Value - indicator.PrevValue

			formatStr := ""
			if progressValue > 0 {
				formatStr = "(+%d)"
			} else {
				formatStr = "(%d)"
			}

			if noColor {
				res = res + fmt.Sprintf(formatStr, progressValue)
			} else {
				res = res + color.New(color.FgGreen).Sprintf(formatStr, progressValue)
			}
		}
	}

	return res
}
