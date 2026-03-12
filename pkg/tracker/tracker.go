package tracker

import (
	"context"
	"errors"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/werf/kubedog/pkg/dyntracker/util"
	"github.com/werf/kubedog/pkg/informer"
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
	Kube                            kubernetes.Interface
	Namespace                       string
	ResourceName                    string
	FullResourceName                string // full resource name with resource kind (deploy/superapp)
	SaveLogsOnlyForNumberOfReplicas int
	LogsFromTime                    time.Time
	InformerFactory                 *util.Concurrent[*informer.InformerFactory]

	StatusGeneration uint64
}

type Options struct {
	ParentContext                            context.Context
	Timeout                                  time.Duration
	LogsFromTime                             time.Time
	SaveLogsOnlyForNumberOfReplicas          int
	IgnoreLogs                               bool
	IgnoreReadinessProbeFailsByContainerName map[string]time.Duration
}

