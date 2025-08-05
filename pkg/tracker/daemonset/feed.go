package daemonset

import (
	"context"
	"sync"

	"k8s.io/client-go/kubernetes"
	watchtools "k8s.io/client-go/tools/watch"

	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/tracker/controller"
)

type Feed interface {
	controller.ControllerFeed

	OnStatus(func(DaemonSetStatus) error)

	GetStatus() DaemonSetStatus
	Track(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error
}

func NewFeed() Feed {
	return &feed{}
}

type feed struct {
	controller.CommonControllerFeed

	OnStatusFunc func(DaemonSetStatus) error

	statusMux sync.Mutex
	status    DaemonSetStatus
}

func (f *feed) OnStatus(function func(DaemonSetStatus) error) {
	f.OnStatusFunc = function
}

func (f *feed) Track(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	errorChan := make(chan error)
	doneChan := make(chan bool)

	parentContext := opts.ParentContext
	if parentContext == nil {
		parentContext = context.Background()
	}
	ctx, cancel := watchtools.ContextWithOptionalTimeout(parentContext, opts.Timeout)
	defer cancel()

	daemonSetTracker := NewTracker(name, namespace, kube, nil, opts)

	go func() {
		err := daemonSetTracker.Track(ctx)
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- true
		}
	}()

	for {
		select {
		case status := <-daemonSetTracker.Added:
			f.setStatus(status)

			if f.OnAddedFunc != nil {
				err := f.OnAddedFunc(status.IsReady)
				if err == tracker.ErrStopTrack {
					return nil
				}
				if err != nil {
					return err
				}

			}

		case status := <-daemonSetTracker.Ready:
			f.setStatus(status)

			if f.OnReadyFunc != nil {
				err := f.OnReadyFunc()
				if err == tracker.ErrStopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case status := <-daemonSetTracker.Failed:
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

		case msg := <-daemonSetTracker.EventMsg:
			if f.OnEventMsgFunc != nil {
				err := f.OnEventMsgFunc(msg)
				if err == tracker.ErrStopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case report := <-daemonSetTracker.AddedPod:
			f.setStatus(report.DaemonSetStatus)

			if f.OnAddedPodFunc != nil {
				err := f.OnAddedPodFunc(report.Pod)
				if err == tracker.ErrStopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case chunk := <-daemonSetTracker.PodLogChunk:
			if f.OnPodLogChunkFunc != nil {
				err := f.OnPodLogChunkFunc(chunk)
				if err == tracker.ErrStopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case report := <-daemonSetTracker.PodError:
			f.setStatus(report.DaemonSetStatus)

			if f.OnPodErrorFunc != nil {
				err := f.OnPodErrorFunc(report.PodError)
				if err == tracker.ErrStopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case status := <-daemonSetTracker.Status:
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

func (f *feed) setStatus(status DaemonSetStatus) {
	f.statusMux.Lock()
	defer f.statusMux.Unlock()
	f.status = status
}

func (f *feed) GetStatus() DaemonSetStatus {
	f.statusMux.Lock()
	defer f.statusMux.Unlock()
	return f.status
}
