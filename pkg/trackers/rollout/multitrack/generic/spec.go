package generic

import (
	"fmt"
	"time"

	"github.com/werf/kubedog/pkg/tracker/resid"
)

type TrackTerminationMode string

const (
	WaitUntilResourceReady TrackTerminationMode = "WaitUntilResourceReady"
	NonBlocking            TrackTerminationMode = "NonBlocking"
)

type FailMode string

const (
	IgnoreAndContinueDeployProcess    FailMode = "IgnoreAndContinueDeployProcess"
	FailWholeDeployProcessImmediately FailMode = "FailWholeDeployProcessImmediately"
	HopeUntilEndOfDeployProcess       FailMode = "HopeUntilEndOfDeployProcess"
)

type Spec struct {
	*resid.ResourceID

	Timeout              time.Duration
	NoActivityTimeout    *time.Duration
	TrackTerminationMode TrackTerminationMode
	FailMode             FailMode
	AllowFailuresCount   *int
	ShowServiceMessages  bool
	HideEvents           bool
	StatusProgressPeriod time.Duration
}

func (s *Spec) Init() error {
	if s.Name == "" {
		return fmt.Errorf("resource can't be nil")
	}

	if s.GroupVersionKind.Kind == "" {
		return fmt.Errorf("resource kind can't be empty")
	}

	if s.NoActivityTimeout == nil {
		s.NoActivityTimeout = new(time.Duration)
		*s.NoActivityTimeout = time.Duration(4 * time.Minute)
	}

	if s.TrackTerminationMode == "" {
		s.TrackTerminationMode = WaitUntilResourceReady
	}

	if s.FailMode == "" {
		s.FailMode = FailWholeDeployProcessImmediately
	}

	if s.AllowFailuresCount == nil {
		s.AllowFailuresCount = new(int)
		*s.AllowFailuresCount = 1
	}

	return nil
}
