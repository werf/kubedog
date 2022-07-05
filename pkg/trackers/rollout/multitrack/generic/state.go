package generic

import (
	"sync"

	"github.com/werf/kubedog/pkg/tracker/generic"
)

type ResourceState string

const (
	ResourceStateActive            ResourceState = "ResourceStateActive"
	ResourceStateSucceeded         ResourceState = "ResourceStateSucceeded"
	ResourceStateFailed            ResourceState = "ResourceStateFailed"
	ResourceStateHoping            ResourceState = "ResourceStateHoping"
	ResourceStateActiveAfterHoping ResourceState = "ResourceStateActiveAfterHoping"
)

type State struct {
	resourceState ResourceState

	lastStatus        *generic.ResourceStatus
	lastPrintedStatus *generic.ResourceStatus

	failuresCount int
	failedReason  string

	mux sync.Mutex
}

func NewState() *State {
	state := &State{}
	state.SetResourceState(ResourceStateActive)

	return state
}

func (s *State) ResourceState() ResourceState {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.resourceState
}

func (s *State) SetResourceState(status ResourceState) {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.resourceState = status
}

func (s *State) LastStatus() *generic.ResourceStatus {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.lastStatus
}

func (s *State) SetLastStatus(status *generic.ResourceStatus) {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.lastStatus = status
}

func (s *State) LastPrintedStatus() *generic.ResourceStatus {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.lastPrintedStatus
}

func (s *State) SetLastPrintedStatus(status *generic.ResourceStatus) {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.lastPrintedStatus = status
}

func (s *State) FailuresCount() int {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.failuresCount
}

func (s *State) BumpFailuresCount() {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.failuresCount++
}

func (s *State) FailedReason() string {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.failedReason
}

func (s *State) SetFailedReason(reason string) {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.failedReason = reason
}
