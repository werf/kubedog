package pod

import (
	"context"
	"fmt"
	"sync"

	"github.com/flant/kubedog/pkg/tracker"
	"github.com/flant/kubedog/pkg/tracker/debug"
	"k8s.io/client-go/kubernetes"
	watchtools "k8s.io/client-go/tools/watch"
)

type Feed interface {
	OnAdded(func() error)
	OnSucceeded(func() error)
	OnFailed(func(string) error)
	OnReady(func() error)

	OnEventMsg(func(msg string) error)
	OnContainerLogChunk(func(*ContainerLogChunk) error)
	OnContainerError(func(ContainerError) error)
	OnStatus(func(PodStatus) error)

	GetStatus() PodStatus
	Track(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error
}

func NewFeed() Feed {
	return &feed{}
}

type feed struct {
	OnAddedFunc             func() error
	OnSucceededFunc         func() error
	OnFailedFunc            func(string) error
	OnEventMsgFunc          func(string) error
	OnReadyFunc             func() error
	OnContainerLogChunkFunc func(*ContainerLogChunk) error
	OnContainerErrorFunc    func(ContainerError) error
	OnStatusFunc            func(PodStatus) error

	statusMux sync.Mutex
	status    PodStatus
}

func (f *feed) OnAdded(function func() error) {
	f.OnAddedFunc = function
}
func (f *feed) OnSucceeded(function func() error) {
	f.OnSucceededFunc = function
}
func (f *feed) OnFailed(function func(string) error) {
	f.OnFailedFunc = function
}
func (f *feed) OnEventMsg(function func(string) error) {
	f.OnEventMsgFunc = function
}
func (f *feed) OnReady(function func() error) {
	f.OnReadyFunc = function
}
func (f *feed) OnContainerLogChunk(function func(*ContainerLogChunk) error) {
	f.OnContainerLogChunkFunc = function
}
func (f *feed) OnContainerError(function func(ContainerError) error) {
	f.OnContainerErrorFunc = function
}
func (f *feed) OnStatus(function func(PodStatus) error) {
	f.OnStatusFunc = function
}

func (f *feed) Track(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	errorChan := make(chan error, 0)
	doneChan := make(chan struct{}, 0)

	parentContext := opts.ParentContext
	if parentContext == nil {
		parentContext = context.Background()
	}
	ctx, cancel := watchtools.ContextWithOptionalTimeout(parentContext, opts.Timeout)
	defer cancel()

	pod := NewTracker(ctx, name, namespace, kube)

	go func() {
		err := pod.Start()
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- struct{}{}
		}
	}()

	for {
		select {
		case chunk := <-pod.ContainerLogChunk:
			if debug.Debug() {
				fmt.Printf("Pod `%s` container `%s` log chunk:\n", pod.ResourceName, chunk.ContainerName)
				for _, line := range chunk.LogLines {
					fmt.Printf("[%s] %s\n", line.Timestamp, line.Message)
				}
			}

			if f.OnContainerLogChunkFunc != nil {
				err := f.OnContainerLogChunkFunc(chunk)
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case report := <-pod.ContainerError:
			f.setStatus(report.PodStatus)

			if f.OnContainerErrorFunc != nil {
				err := f.OnContainerErrorFunc(report.ContainerError)
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case status := <-pod.Added:
			f.setStatus(status)

			if f.OnAddedFunc != nil {
				err := f.OnAddedFunc()
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case status := <-pod.Succeeded:
			f.setStatus(status)

			if f.OnSucceededFunc != nil {
				err := f.OnSucceededFunc()
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case failed := <-pod.Failed:
			f.setStatus(failed.PodStatus)

			if f.OnFailedFunc != nil {
				err := f.OnFailedFunc(failed.FailedReason)
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case msg := <-pod.EventMsg:
			if debug.Debug() {
				fmt.Printf("Pod `%s` event msg: %s\n", pod.ResourceName, msg)
			}

			if f.OnEventMsgFunc != nil {
				err := f.OnEventMsgFunc(msg)
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case status := <-pod.Ready:
			f.setStatus(status)

			if f.OnReadyFunc != nil {
				err := f.OnReadyFunc()
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case status := <-pod.Status:
			f.setStatus(status)

			if f.OnStatusFunc != nil {
				err := f.OnStatusFunc(status)
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case err := <-errorChan:
			return err
		case <-doneChan:
			return nil
		}
	}
}

func (f *feed) setStatus(status PodStatus) {
	f.statusMux.Lock()
	defer f.statusMux.Unlock()
	f.status = status
}

func (f *feed) GetStatus() PodStatus {
	f.statusMux.Lock()
	defer f.statusMux.Unlock()
	return f.status
}
