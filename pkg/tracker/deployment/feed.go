package deployment

import (
	"context"
	"fmt"
	"sync"

	"k8s.io/client-go/kubernetes"
	watchtools "k8s.io/client-go/tools/watch"

	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/tracker/controller"
	"github.com/werf/kubedog/pkg/tracker/debug"
)

type Feed interface {
	controller.ControllerFeed

	OnStatus(func(DeploymentStatus) error)

	GetStatus() DeploymentStatus
	Track(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error
}

func NewFeed() Feed {
	return &feed{}
}

type feed struct {
	controller.CommonControllerFeed

	OnStatusFunc func(DeploymentStatus) error

	statusMux sync.Mutex
	status    DeploymentStatus
}

func (f *feed) OnStatus(function func(DeploymentStatus) error) {
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

	deploymentTracker := NewTracker(name, namespace, kube, opts)

	go func() {
		if debug.Debug() {
			fmt.Printf("  goroutine: start deploy/%s tracker\n", name)
		}
		err := deploymentTracker.Track(ctx)
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- true
		}
	}()

	if debug.Debug() {
		fmt.Printf("  deploy/%s: for-select DeploymentTracker channels\n", name)
	}

	for {
		select {
		case status := <-deploymentTracker.Added:
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

		case status := <-deploymentTracker.Ready:
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

		case status := <-deploymentTracker.Failed:
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

		case msg := <-deploymentTracker.EventMsg:
			if f.OnEventMsgFunc != nil {
				err := f.OnEventMsgFunc(msg)
				if err == tracker.ErrStopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case report := <-deploymentTracker.AddedReplicaSet:
			f.setStatus(report.DeploymentStatus)

			if f.OnAddedReplicaSetFunc != nil {
				err := f.OnAddedReplicaSetFunc(report.ReplicaSet)
				if err == tracker.ErrStopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case report := <-deploymentTracker.AddedPod:
			f.setStatus(report.DeploymentStatus)

			if f.OnAddedPodFunc != nil {
				err := f.OnAddedPodFunc(report.ReplicaSetPod)
				if err == tracker.ErrStopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case chunk := <-deploymentTracker.PodLogChunk:
			if debug.Debug() {
				fmt.Printf("    deploy/%s pod `%s` log chunk\n", deploymentTracker.ResourceName, chunk.PodName)
				for _, line := range chunk.LogLines {
					fmt.Printf("po/%s [%s] %s\n", chunk.PodName, line.Timestamp, line.Message)
				}
			}

			if f.OnPodLogChunkFunc != nil {
				err := f.OnPodLogChunkFunc(chunk)
				if err == tracker.ErrStopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case report := <-deploymentTracker.PodError:
			f.setStatus(report.DeploymentStatus)

			if f.OnPodErrorFunc != nil {
				err := f.OnPodErrorFunc(report.ReplicaSetPodError)
				if err == tracker.ErrStopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case status := <-deploymentTracker.Status:
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

func (f *feed) setStatus(status DeploymentStatus) {
	f.statusMux.Lock()
	defer f.statusMux.Unlock()
	f.status = status
}

func (f *feed) GetStatus() DeploymentStatus {
	f.statusMux.Lock()
	defer f.statusMux.Unlock()
	return f.status
}
