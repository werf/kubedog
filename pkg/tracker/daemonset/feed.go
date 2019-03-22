package daemonset

import (
	"context"
	"fmt"
	"sync"

	"github.com/flant/kubedog/pkg/tracker"
	"github.com/flant/kubedog/pkg/tracker/controller"
	"github.com/flant/kubedog/pkg/tracker/debug"
	"k8s.io/client-go/kubernetes"

	watchtools "k8s.io/client-go/tools/watch"
)

type Feed interface {
	controller.ControllerFeed

	OnStatusReport(func(DaemonSetStatus) error)
	GetStatus() DaemonSetStatus
	Track(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error
}

func NewFeed() Feed {
	return &feed{}
}

type feed struct {
	controller.CommonControllerFeed
	OnStatusReportFunc func(DaemonSetStatus) error

	statusMux sync.Mutex
	status    DaemonSetStatus
}

func (f *feed) OnStatusReport(function func(DaemonSetStatus) error) {
	f.OnStatusReportFunc = function
}

func (f *feed) Track(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error {
	errorChan := make(chan error, 0)
	doneChan := make(chan bool, 0)

	parentContext := opts.ParentContext
	if parentContext == nil {
		parentContext = context.Background()
	}
	ctx, cancel := watchtools.ContextWithOptionalTimeout(parentContext, opts.Timeout)
	defer cancel()

	daemonSetTracker := NewTracker(ctx, name, namespace, kube, opts)

	go func() {
		if debug.Debug() {
			fmt.Printf("  goroutine: start DaemonSet/%s tracker\n", name)
		}
		err := daemonSetTracker.Track()
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- true
		}
	}()

	if debug.Debug() {
		fmt.Printf("  ds/%s: for-select DaemonSetTracker channels\n", name)
	}

	for {
		select {
		case isReady := <-daemonSetTracker.Added:
			if debug.Debug() {
				fmt.Printf("    ds/%s added\n", name)
			}

			if f.OnAddedFunc != nil {
				err := f.OnAddedFunc(isReady)
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}

			}

		case <-daemonSetTracker.Ready:
			if debug.Debug() {
				fmt.Printf("    ds/%s ready: desired: %d, current: %d, updated: %d, ready: %d\n",
					name,
					daemonSetTracker.FinalDaemonSetStatus.DesiredNumberScheduled,
					daemonSetTracker.FinalDaemonSetStatus.CurrentNumberScheduled,
					daemonSetTracker.FinalDaemonSetStatus.UpdatedNumberScheduled,
					daemonSetTracker.FinalDaemonSetStatus.NumberReady,
				)
			}

			if f.OnReadyFunc != nil {
				err := f.OnReadyFunc()
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case reason := <-daemonSetTracker.Failed:
			if debug.Debug() {
				fmt.Printf("    ds/%s failed. Tracker state: `%s`", name, daemonSetTracker.State)
			}

			if f.OnFailedFunc != nil {
				err := f.OnFailedFunc(reason)
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case msg := <-daemonSetTracker.EventMsg:
			if debug.Debug() {
				fmt.Printf("    ds/%s event: %s\n", name, msg)
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

		case pod := <-daemonSetTracker.AddedPod:
			if debug.Debug() {
				fmt.Printf("    ds/%s po/%s added\n", daemonSetTracker.ResourceName, pod.Name)
			}

			if f.OnAddedPodFunc != nil {
				err := f.OnAddedPodFunc(pod)
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case chunk := <-daemonSetTracker.PodLogChunk:
			if debug.Debug() {
				fmt.Printf("    ds/%s po/%s log chunk\n", daemonSetTracker.ResourceName, chunk.PodName)
				for _, line := range chunk.LogLines {
					fmt.Printf("po/%s [%s] %s\n", chunk.PodName, line.Timestamp, line.Message)
				}
			}

			if f.OnPodLogChunkFunc != nil {
				err := f.OnPodLogChunkFunc(chunk)
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case podError := <-daemonSetTracker.PodError:
			if debug.Debug() {
				fmt.Printf("    ds/%s pod error: %s\n", daemonSetTracker.ResourceName, podError.Message)
			}

			if f.OnPodErrorFunc != nil {
				err := f.OnPodErrorFunc(podError)
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case status := <-daemonSetTracker.StatusReport:
			f.setStatus(status)

			if f.OnStatusReportFunc != nil {
				err := f.OnStatusReportFunc(status)
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case err := <-errorChan:
			return fmt.Errorf("ds/%s error: %v", name, err)
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
