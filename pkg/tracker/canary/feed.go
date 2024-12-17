package canary

import (
	"context"
	"sync"

	"k8s.io/client-go/kubernetes"
	watchtools "k8s.io/client-go/tools/watch"

	"github.com/werf/kubedog/for-werf-helm/pkg/tracker"
)

type Feed interface {
	OnAdded(func() error)
	OnSucceeded(func() error)
	OnFailed(func(reason string) error)
	OnEventMsg(func(msg string) error)
	OnStatus(func(CanaryStatus) error)

	GetStatus() CanaryStatus
	Track(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error
}

func NewFeed() Feed {
	return &feed{}
}

type feed struct {
	OnAddedFunc     func() error
	OnSucceededFunc func() error
	OnFailedFunc    func(string) error
	OnEventMsgFunc  func(string) error
	OnStatusFunc    func(CanaryStatus) error

	statusMux sync.Mutex
	status    CanaryStatus
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

func (f *feed) OnStatus(function func(CanaryStatus) error) {
	f.OnStatusFunc = function
}

func (f *feed) Track(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	errorChan := make(chan error)
	doneChan := make(chan struct{})

	parentContext := opts.ParentContext
	if parentContext == nil {
		parentContext = context.Background()
	}
	ctx, cancel := watchtools.ContextWithOptionalTimeout(parentContext, opts.Timeout)
	defer cancel()

	canary := NewTracker(name, namespace, kube, opts)

	go func() {
		err := canary.Track(ctx)
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- struct{}{}
		}
	}()

	for {
		select {
		case status := <-canary.Added:
			f.setStatus(status)

			if f.OnAddedFunc != nil {
				err := f.OnAddedFunc()
				if err == tracker.ErrStopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}
		case status := <-canary.Succeeded:
			f.setStatus(status)

			if f.OnSucceededFunc != nil {
				err := f.OnSucceededFunc()
				if err == tracker.ErrStopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case status := <-canary.Failed:
			f.setStatus(status)

			if f.OnFailedFunc != nil {
				err := f.OnFailedFunc(status.FailedReason)
				if err == tracker.ErrStopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case msg := <-canary.EventMsg:
			if f.OnEventMsgFunc != nil {
				err := f.OnEventMsgFunc(msg)
				if err == tracker.ErrStopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case status := <-canary.Status:
			f.setStatus(status)

			if f.OnStatusFunc != nil {
				err := f.OnStatusFunc(status)
				if err == tracker.ErrStopTrack {
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

func (f *feed) setStatus(status CanaryStatus) {
	f.statusMux.Lock()
	defer f.statusMux.Unlock()

	if status.StatusGeneration > f.status.StatusGeneration {
		f.status = status
	}
}

func (f *feed) GetStatus() CanaryStatus {
	f.statusMux.Lock()
	defer f.statusMux.Unlock()
	return f.status
}
