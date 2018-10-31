package monitor

import (
	"context"
	"errors"
	"os"
	"time"

	"k8s.io/client-go/kubernetes"
)

var (
	ErrWatchTimeout = errors.New("timed out watching resource")
	StopWatch       = errors.New("stop watch monitor now")
)

type WatchMonitor struct {
	Kube          kubernetes.Interface
	Namespace     string
	ResourceName  string
	Context       context.Context
	ContextCancel context.CancelFunc
}

type WatchOptions struct {
	ParentContext   context.Context
	Timeout         time.Duration
	WaitForResource bool
}

type WatchMonitorState string

func debug() bool {
	return os.Getenv("KUBEDOG_MONITOR_DEBUG") == "1"
}
