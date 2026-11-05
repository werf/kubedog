//go:build ai_tests

package resource

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/werf/kubedog/pkg/tracker"
)

func TestAI_NextState(t *testing.T) {
	tests := []struct {
		name           string
		state          tracker.TrackerState
		ready          bool
		failed         bool
		wantState      tracker.TrackerState
		wantTransition Transition
	}{
		{"initial progressing", tracker.Initial, false, false, tracker.ResourceAdded, TransitionAdded},
		{"initial ready", tracker.Initial, true, false, tracker.ResourceReady, TransitionReady},
		{"initial failed", tracker.Initial, false, true, tracker.ResourceFailed, TransitionFailed},
		{"ready wins over failed", tracker.Initial, true, true, tracker.ResourceReady, TransitionReady},
		{"added progressing", tracker.ResourceAdded, false, false, tracker.ResourceAdded, TransitionStatus},
		{"added ready", tracker.ResourceAdded, true, false, tracker.ResourceReady, TransitionReady},
		{"added failed", tracker.ResourceAdded, false, true, tracker.ResourceFailed, TransitionFailed},
		{"failed still failed", tracker.ResourceFailed, false, true, tracker.ResourceFailed, TransitionFailed},
		{"failed recovered", tracker.ResourceFailed, true, false, tracker.ResourceReady, TransitionReady},
		{"failed progressing", tracker.ResourceFailed, false, false, tracker.ResourceFailed, TransitionStatus},
		{"deleted recreated", tracker.ResourceDeleted, false, false, tracker.ResourceAdded, TransitionAdded},
		{"succeeded", tracker.ResourceSucceeded, true, false, tracker.ResourceSucceeded, TransitionStatus},
		{"ready is final", tracker.ResourceReady, false, false, tracker.ResourceReady, TransitionNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, transition := NextState(tt.state, tt.ready, tt.failed)
			assert.Equal(t, tt.wantState, state)
			assert.Equal(t, tt.wantTransition, transition)
		})
	}
}
