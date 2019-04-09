package debug

import "os"

func YesNo(v bool) string {
	if v {
		return "YES"
	}
	return " no"
}

func Debug() bool {
	return os.Getenv("KUBEDOG_TRACKER_DEBUG") == "1"
}
