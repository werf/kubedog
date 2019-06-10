package tracker

import (
	"context"
	"errors"
	"fmt"
	"time"

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
