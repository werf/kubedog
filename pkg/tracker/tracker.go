package tracker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"k8s.io/client-go/kubernetes"
)

var (
	StopTrack = errors.New("stop tracking now")
)

const (
	Initial           TrackerState = ""
	ResourceAdded     TrackerState = "ResourceAdded"
	ResourceSucceeded TrackerState = "ResourceSucceeded"
	ResourceReady     TrackerState = "ResourceReady"
	ResourceFailed    TrackerState = "ResourceFailed"
	ResourceDeleted   TrackerState = "ResourceDeleted"

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

	StatusGeneration uint64
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
