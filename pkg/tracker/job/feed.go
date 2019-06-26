package job

import (
	"context"
	"fmt"
	"sync"

	"github.com/flant/kubedog/pkg/tracker"
	"github.com/flant/kubedog/pkg/tracker/debug"
	"github.com/flant/kubedog/pkg/tracker/pod"
	"k8s.io/client-go/kubernetes"

	watchtools "k8s.io/client-go/tools/watch"
)

type Feed interface {
	OnAdded(func() error)
	OnSucceeded(func() error)
	OnFailed(func(reason string) error)
	OnEventMsg(func(msg string) error)
	OnAddedPod(func(podName string) error)
	OnPodLogChunk(func(*pod.PodLogChunk) error)
	OnPodError(func(pod.PodError) error)
	OnStatusReport(func(JobStatus) error)

	GetStatus() JobStatus
	Track(name, namespace string, kube kubernetes.Interface, opts tracker.Options) error
}

func NewFeed() Feed {
	return &feed{}
}

type feed struct {
	OnAddedFunc        func() error
	OnSucceededFunc    func() error
	OnFailedFunc       func(string) error
	OnEventMsgFunc     func(string) error
	OnAddedPodFunc     func(string) error
	OnPodLogChunkFunc  func(*pod.PodLogChunk) error
	OnPodErrorFunc     func(pod.PodError) error
	OnStatusReportFunc func(JobStatus) error

	statusMux sync.Mutex
	status    JobStatus
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
func (f *feed) OnAddedPod(function func(string) error) {
	f.OnAddedPodFunc = function
}
func (f *feed) OnPodLogChunk(function func(*pod.PodLogChunk) error) {
	f.OnPodLogChunkFunc = function
}
func (f *feed) OnPodError(function func(pod.PodError) error) {
	f.OnPodErrorFunc = function
}
func (f *feed) OnStatusReport(function func(JobStatus) error) {
	f.OnStatusReportFunc = function
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

	job := NewTracker(ctx, name, namespace, kube)

	go func() {
		err := job.Track()
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- struct{}{}
		}
	}()

	for {
		select {
		case <-job.Added:
			if debug.Debug() {
				fmt.Printf("Job `%s` added\n", job.ResourceName)
			}

			if f.OnAddedFunc != nil {
				err := f.OnAddedFunc()
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case status := <-job.Succeeded:
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

		case reason := <-job.Failed:
			if debug.Debug() {
				fmt.Printf("Job `%s` failed: %s\n", job.ResourceName, reason)
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

		case msg := <-job.EventMsg:
			if debug.Debug() {
				fmt.Printf("Job `%s` event msg: %s\n", job.ResourceName, msg)
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

		case podName := <-job.AddedPod:
			if debug.Debug() {
				fmt.Printf("Job's `%s` pod `%s` added\n", job.ResourceName, podName)
			}

			if f.OnAddedPodFunc != nil {
				err := f.OnAddedPodFunc(podName)
				if err == tracker.StopTrack {
					return nil
				}
				if err != nil {
					return err
				}
			}

		case chunk := <-job.PodLogChunk:
			if debug.Debug() {
				fmt.Printf("Job's `%s` pod `%s` log chunk\n", job.ResourceName, chunk.PodName)
				for _, line := range chunk.LogLines {
					fmt.Printf("[%s] %s\n", line.Timestamp, line.Message)
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

		case podError := <-job.PodError:
			if debug.Debug() {
				fmt.Printf("Job's `%s` pod error: %#v", job.ResourceName, podError)
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

		case status := <-job.StatusReport:
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
			return err
		case <-doneChan:
			return nil
		}
	}
}

func (f *feed) setStatus(status JobStatus) {
	f.statusMux.Lock()
	defer f.statusMux.Unlock()

	if status.StatusGeneration > f.status.StatusGeneration {
		f.status = status
	}
}

func (f *feed) GetStatus() JobStatus {
	f.statusMux.Lock()
	defer f.statusMux.Unlock()
	return f.status
}
