package debug

import "os"

var debug *bool

func YesNo(v bool) string {
	if v {
		return "YES"
	}
	return " no"
}

func Debug() bool {
	if debug != nil {
		return *debug
	}

	return os.Getenv("KUBEDOG_TRACKER_DEBUG") == "1"
}

func SetDebug(v bool) {
	debug = &v
}
