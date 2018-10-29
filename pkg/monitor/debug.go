package monitor

import "os"

func debug() bool {
	return os.Getenv("KUBEDOG_MONITOR_DEBUG") == "1"
}
