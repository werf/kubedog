package tracker

// ControllerFeed interface for rollout process callbacks
type ControllerFeed interface {
	Added(ready bool) error
	Ready() error
	Failed(reason string) error
	EventMsg(msg string) error // reason: msg  // Pulling: pull alpine:3.6....
	AddedReplicaSet(ReplicaSet) error
	AddedPod(ReplicaSetPod) error
	PodLogChunk(*ReplicaSetPodLogChunk) error
	PodError(ReplicaSetPodError) error
}
