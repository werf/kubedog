package resource

import (
	"github.com/werf/kubedog/pkg/tracker"
)

// Transition is what a tracker reports after it observed a new state of the resource.
type Transition string

const (
	TransitionNone   Transition = ""
	TransitionAdded  Transition = "Added"
	TransitionReady  Transition = "Ready"
	TransitionFailed Transition = "Failed"
	TransitionStatus Transition = "Status"
)

// NextState returns the state a tracker moves to after it observed the resource with the
// given readiness, and the transition the tracker should report. It is the state machine
// shared by all the resource trackers.
func NextState(state tracker.TrackerState, ready, failed bool) (tracker.TrackerState, Transition) {
	switch state {
	case tracker.Initial, tracker.ResourceDeleted:
		switch {
		case ready:
			return tracker.ResourceReady, TransitionReady
		case failed:
			return tracker.ResourceFailed, TransitionFailed
		default:
			return tracker.ResourceAdded, TransitionAdded
		}
	case tracker.ResourceAdded, tracker.ResourceFailed:
		switch {
		case ready:
			return tracker.ResourceReady, TransitionReady
		case failed:
			return tracker.ResourceFailed, TransitionFailed
		default:
			return state, TransitionStatus
		}
	case tracker.ResourceSucceeded:
		return state, TransitionStatus
	default:
		return state, TransitionNone
	}
}
