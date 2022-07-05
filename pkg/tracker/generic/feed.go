package generic

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	watchtools "k8s.io/client-go/tools/watch"

	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/tracker/debug"
)

type Feed struct {
	tracker *Tracker

	onAddedFunc    func(status *ResourceStatus) error
	onReadyFunc    func(status *ResourceStatus) error
	onFailedFunc   func(status *ResourceStatus) error
	onStatusFunc   func(status *ResourceStatus) error
	onEventMsgFunc func(event *corev1.Event) error
}

func NewFeed(tracker *Tracker) *Feed {
	return &Feed{
		tracker: tracker,
	}
}

func (f *Feed) OnAdded(function func(status *ResourceStatus) error) {
	f.onAddedFunc = function
}

func (f *Feed) OnReady(function func(status *ResourceStatus) error) {
	f.onReadyFunc = function
}

func (f *Feed) OnFailed(function func(status *ResourceStatus) error) {
	f.onFailedFunc = function
}

func (f *Feed) OnStatus(function func(status *ResourceStatus) error) {
	f.onStatusFunc = function
}

func (f *Feed) OnEventMsg(function func(event *corev1.Event) error) {
	f.onEventMsgFunc = function
}

func (f *Feed) Track(ctx context.Context, timeout, noActivityTimeout time.Duration) error {
	ctx, cancelFunc := watchtools.ContextWithOptionalTimeout(ctx, timeout)
	defer cancelFunc()

	addedCh := make(chan *ResourceStatus)
	succeededCh := make(chan *ResourceStatus)
	failedCh := make(chan *ResourceStatus)
	regularCh := make(chan *ResourceStatus, 100)
	eventCh := make(chan *corev1.Event)
	errCh := make(chan error, 10)

	go func() {
		if debug.Debug() {
			fmt.Printf("  goroutine: start %s tracker\n", f.tracker.ResourceID)
		}

		errCh <- f.tracker.Track(ctx, noActivityTimeout, addedCh, succeededCh, failedCh, regularCh, eventCh)
	}()

	for {
		select {
		case status := <-addedCh:
			if f.onAddedFunc != nil {
				if err := f.onAddedFunc(status); errors.Is(err, tracker.ErrStopTrack) {
					return nil
				} else if err != nil {
					return err
				}
			}
		case status := <-succeededCh:
			if f.onReadyFunc != nil {
				if err := f.onReadyFunc(status); errors.Is(err, tracker.ErrStopTrack) {
					return nil
				} else if err != nil {
					return err
				}
			}
		case status := <-failedCh:
			if f.onFailedFunc != nil {
				if err := f.onFailedFunc(status); errors.Is(err, tracker.ErrStopTrack) {
					return nil
				} else if err != nil {
					return err
				}
			}
		case status := <-regularCh:
			if f.onStatusFunc != nil {
				if err := f.onStatusFunc(status); errors.Is(err, tracker.ErrStopTrack) {
					return nil
				} else if err != nil {
					return err
				}
			}
		case event := <-eventCh:
			if f.onEventMsgFunc != nil {
				if err := f.onEventMsgFunc(event); errors.Is(err, tracker.ErrStopTrack) {
					return nil
				} else if err != nil {
					return err
				}
			}
		case err := <-errCh:
			return err
		}
	}
}
