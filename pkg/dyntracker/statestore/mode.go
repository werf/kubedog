package statestore

type TrackTerminationMode string

const (
	WaitUntilResourceReady TrackTerminationMode = "WaitUntilResourceReady"
	NonBlocking            TrackTerminationMode = "NonBlocking"
)

type FailMode string

const (
	IgnoreAndContinueDeployProcess    FailMode = "IgnoreAndContinueDeployProcess"
	FailWholeDeployProcessImmediately FailMode = "FailWholeDeployProcessImmediately"
	// TODO: get rid. Is an equivalent to FailWholeDeployProcessImmediately at the moment. Or should we? We might want
	// to reimplement some things in kubedog, and this feature might makes sense once again.
	LegacyHopeUntilEndOfDeployProcess FailMode = "HopeUntilEndOfDeployProcess"
)
