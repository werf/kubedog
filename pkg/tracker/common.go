package tracker

import (
	"context"
	"errors"
	"os"
	"time"

	"k8s.io/client-go/kubernetes"
)

var (
	ErrTrackTimeout = errors.New("timed out tracking resource")
	StopTrack       = errors.New("stop tracking now")
)

const (
	Initial             TrackerState = "Initial"
	ResourceAdded       TrackerState = "ResourceAdded"
	ResourceSucceeded   TrackerState = "ResourceSucceeded"
	ResourceFailed      TrackerState = "ResourceFailed"
	ContainerRunning    TrackerState = "ContainerRunning"
	ContainerWaiting    TrackerState = "ContainerWaiting"
	ContainerTerminated TrackerState = "ContainerTerminated"
)

type TrackerState string

type Tracker struct {
	Kube          kubernetes.Interface
	Namespace     string
	ResourceName  string
	Context       context.Context
	ContextCancel context.CancelFunc
}

type Options struct {
	ParentContext context.Context
	Timeout       time.Duration
	LogsFromTime  time.Time
}

func debug() bool {
	return os.Getenv("KUBEDOG_TRACKER_DEBUG") == "1"
}
