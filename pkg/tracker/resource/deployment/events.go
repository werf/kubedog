package deployment

import (
	"github.com/werf/kubedog/pkg/tracker/pod"
)

// Event is reported by the Tracker. All the events come through a single channel, so their
// order is the order in which the Tracker observed them.
type Event interface {
	isEvent()
}

// AddedEvent is reported when the Deployment is observed for the first time (or after it
// was deleted) and is not ready yet.
type AddedEvent struct{ Status Status }

type ReadyEvent struct{ Status Status }

// FailedEvent is reported when the Deployment failed. Status.Object is nil if the failure
// was reported before the Deployment itself was observed.
type FailedEvent struct{ Status Status }

// StatusEvent is reported when the state of the Deployment or of its Pods changed.
type StatusEvent struct{ Status Status }

type DeletedEvent struct{}

type ReplicaSetAddedEvent struct {
	ReplicaSet ReplicaSet
	Status     Status
}

type PodAddedEvent struct {
	PodName    string
	ReplicaSet ReplicaSet
	Status     Status
}

type PodLogChunkEvent struct {
	PodName    string
	ReplicaSet ReplicaSet
	Chunk      *pod.ContainerLogChunk
}

type PodErrorEvent struct {
	PodName        string
	ReplicaSet     ReplicaSet
	ContainerError pod.ContainerError
	Status         Status
}

// MessageEvent is a Kubernetes event message about the Deployment or one of its Pods.
type MessageEvent struct{ Message string }

func (AddedEvent) isEvent()           {}
func (ReadyEvent) isEvent()           {}
func (FailedEvent) isEvent()          {}
func (StatusEvent) isEvent()          {}
func (DeletedEvent) isEvent()         {}
func (ReplicaSetAddedEvent) isEvent() {}
func (PodAddedEvent) isEvent()        {}
func (PodLogChunkEvent) isEvent()     {}
func (PodErrorEvent) isEvent()        {}
func (MessageEvent) isEvent()         {}
