package log

import "os"

var debug *bool

func Debug() bool {
	if debug != nil {
		return *debug
	}

	return os.Getenv("KUBEDOG_TRACKER_DEBUG") == "1"
}

func SetDebug(v bool) {
	debug = &v
}
