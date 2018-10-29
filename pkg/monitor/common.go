package monitor

import (
	"os"
	"time"

	"k8s.io/client-go/kubernetes"
)

type WatchMonitor struct {
	Kube         kubernetes.Interface
	Timeout      time.Duration
	Namespace    string
	ResourceName string
}

type WatchOptions struct {
	Timeout time.Duration
}

func debug() bool {
	return os.Getenv("KUBEDOG_MONITOR_DEBUG") == "1"
}
