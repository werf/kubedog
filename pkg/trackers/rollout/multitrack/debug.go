package multitrack

import "os"

func debug() bool {
	return os.Getenv("KUBEDOG_ROLLOUT_MULTITRACK_DEBUG") == "1"
}
