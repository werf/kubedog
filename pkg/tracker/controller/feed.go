package controller

import "github.com/flant/kubedog/pkg/tracker/replicaset"

type ControllerFeed interface {
	OnAdded(func(ready bool) error)
	OnReady(func() error)
	OnFailed(func(reason string) error)
	OnEventMsg(func(msg string) error) // Pulling: pull alpine:3.6....
	OnAddedReplicaSet(func(replicaset.ReplicaSet) error)
	OnAddedPod(func(replicaset.ReplicaSetPod) error)
	OnPodLogChunk(func(*replicaset.ReplicaSetPodLogChunk) error)
	OnPodError(func(replicaset.ReplicaSetPodError) error)
}

type CommonControllerFeed struct {
	OnAddedFunc           func(bool) error
	OnReadyFunc           func() error
	OnFailedFunc          func(reason string) error
	OnEventMsgFunc        func(msg string) error
	OnAddedReplicaSetFunc func(replicaset.ReplicaSet) error
	OnAddedPodFunc        func(replicaset.ReplicaSetPod) error
	OnPodLogChunkFunc     func(*replicaset.ReplicaSetPodLogChunk) error
	OnPodErrorFunc        func(replicaset.ReplicaSetPodError) error
}

func (f *CommonControllerFeed) OnAdded(function func(bool) error) {
	f.OnAddedFunc = function
}
func (f *CommonControllerFeed) OnReady(function func() error) {
	f.OnReadyFunc = function
}
func (f *CommonControllerFeed) OnFailed(function func(string) error) {
	f.OnFailedFunc = function
}
func (f *CommonControllerFeed) OnEventMsg(function func(string) error) {
	f.OnEventMsgFunc = function
}
func (f *CommonControllerFeed) OnAddedReplicaSet(function func(replicaset.ReplicaSet) error) {
	f.OnAddedReplicaSetFunc = function
}
func (f *CommonControllerFeed) OnAddedPod(function func(replicaset.ReplicaSetPod) error) {
	f.OnAddedPodFunc = function
}
func (f *CommonControllerFeed) OnPodLogChunk(function func(*replicaset.ReplicaSetPodLogChunk) error) {
	f.OnPodLogChunkFunc = function
}
func (f *CommonControllerFeed) OnPodError(function func(replicaset.ReplicaSetPodError) error) {
	f.OnPodErrorFunc = function
}
