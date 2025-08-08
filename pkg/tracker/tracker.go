package tracker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"

	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
)

var ErrStopTrack = errors.New("stop tracking now")

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
	LogsFromTime     time.Time
	InformerFactory  *util.Concurrent[*informer.InformerFactory]

	StatusGeneration uint64
}

type Options struct {
	ParentContext                            context.Context
	Timeout                                  time.Duration
	LogsFromTime                             time.Time
	IgnoreLogs                               bool
	IgnoreReadinessProbeFailsByContainerName map[string]time.Duration
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

func AdaptInformerError(err error) error {
	if errors.Is(err, wait.ErrWaitTimeout) {
		return nil
	}
	return err
}
