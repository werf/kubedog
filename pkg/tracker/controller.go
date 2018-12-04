package tracker

// ControllerFeed interface for rollout process callbacks
type ControllerFeed interface {
	Added(ready bool) error
	Ready() error
	Failed(reason string) error
	AddedReplicaSet(ReplicaSet) error
	AddedPod(ReplicaSetPod) error
	PodLogChunk(*ReplicaSetPodLogChunk) error
	PodError(ReplicaSetPodError) error
}
