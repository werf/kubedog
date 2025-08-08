package generic

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/tracker/debug"
	"github.com/werf/kubedog/pkg/tracker/resid"
	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
)

const ResourceStatusStabilizingDuration time.Duration = 2 * time.Second

type TrackerState string

const (
	TrackerStateInitial           TrackerState = "TrackerStateInitial"
	TrackerStateStatusStabilizing TrackerState = "TrackerStateStatusStabilizing"
	TrackerStateStarted           TrackerState = "TrackerStateStarted"
	TrackerStateResourceAdded     TrackerState = "TrackerStateResourceAdded"
	TrackerStateResourceSucceeded TrackerState = "TrackerStateResourceSucceeded"
	TrackerStateResourceFailed    TrackerState = "TrackerStateResourceFailed"
	TrackerStateResourceDeleted   TrackerState = "TrackerStateResourceDeleted"
)

type Tracker struct {
	ResourceID *resid.ResourceID

	lastState    TrackerState
	lastStateMux sync.Mutex

	statusStableAt    time.Time
	statusStableAtMux sync.Mutex

	lastObjDuringStatusStabilization    *unstructured.Unstructured
	lastObjDuringStatusStabilizationMux sync.Mutex

	client          kubernetes.Interface
	dynamicClient   dynamic.Interface
	discoveryClient discovery.CachedDiscoveryInterface
	mapper          meta.RESTMapper
	informerFactory *util.Concurrent[*informer.InformerFactory]
}

func NewTracker(
	resID *resid.ResourceID,
	client kubernetes.Interface,
	dynClient dynamic.Interface,
	discClient discovery.CachedDiscoveryInterface,
	informerFactory *util.Concurrent[*informer.InformerFactory],
	mapper meta.RESTMapper,
) *Tracker {
	return &Tracker{
		ResourceID:      resID,
		lastState:       TrackerStateInitial,
		client:          client,
		dynamicClient:   dynClient,
		discoveryClient: discClient,
		mapper:          mapper,
		informerFactory: informerFactory,
	}
}

func (t *Tracker) Track(ctx context.Context, noActivityTimeout time.Duration, addedCh, succeededCh, failedCh, regularCh chan<- *ResourceStatus, eventCh chan<- *corev1.Event) error {
	resAddedCh := make(chan *unstructured.Unstructured)
	resModifiedCh := make(chan *unstructured.Unstructured)
	resDeletedCh := make(chan *unstructured.Unstructured)

	resourceStateWatcher := NewResourceStateWatcher(t.ResourceID, t.client, t.dynamicClient, t.mapper, t.informerFactory)
	resourceInformerCleanupFn, err := resourceStateWatcher.Run(ctx, resAddedCh, resModifiedCh, resDeletedCh)
	if err != nil {
		return fmt.Errorf("run resource state watcher: %w", err)
	}
	defer resourceInformerCleanupFn()

	statusStabilizingTicker := time.NewTicker(time.Second)
	statusStabilizingDoneCh := make(chan bool)
	defer func() {
		statusStabilizingTicker.Stop()
		close(statusStabilizingDoneCh)
	}()
	go func() {
		for {
			select {
			case <-statusStabilizingTicker.C:
				if t.handleStatusStabilized(resModifiedCh) {
					statusStabilizingTicker.Stop()
					return
				}
			case <-statusStabilizingDoneCh:
				return
			}
		}
	}()

	eventWatcherCh := make(chan *corev1.Event)

	for {
		select {
		case obj := <-resAddedCh:
			cleanupFn, err := t.handleResourceAddedModified(ctx, obj, addedCh, succeededCh, failedCh, regularCh, eventWatcherCh)
			if err != nil {
				return fmt.Errorf("handle resource added: %w", err)
			}
			defer cleanupFn()
		case obj := <-resModifiedCh:
			cleanupFn, err := t.handleResourceAddedModified(ctx, obj, addedCh, succeededCh, failedCh, regularCh, eventWatcherCh)
			if err != nil {
				return fmt.Errorf("handle resource modified: %w", err)
			}
			defer cleanupFn()
		case <-resDeletedCh:
			t.handleResourceDeleted(regularCh)
		case event := <-eventWatcherCh:
			t.handleEvent(event, eventCh, failedCh)
		case <-time.After(noActivityTimeout):
			failedCh <- NewFailedResourceStatus(fmt.Sprintf("marking resource as failed because no activity for %s", noActivityTimeout))
		case <-ctx.Done():
			if debug.Debug() {
				fmt.Printf("`%s` tracker context canceled: %s\n", t.ResourceID, context.Cause(ctx))
			}

			return nil
		}
	}
}

func (t *Tracker) handleStatusStabilized(resModCh chan<- *unstructured.Unstructured) (done bool) {
	if t.statusStabilizeAt().IsZero() {
		return false
	}

	if t.statusStabilizeAt().Before(time.Now()) {
		t.setLastState(TrackerStateStarted)
		resModCh <- t.lastObjectDuringStatusStabilization()
		return true
	}

	return false
}

func (t *Tracker) handleResourceAddedModified(ctx context.Context, object *unstructured.Unstructured, addedCh, succeededCh, failedCh, regularCh chan<- *ResourceStatus, eventCh chan<- *corev1.Event) (cleanupFn func(), err error) {
	cleanupFn = func() {}

	if t.getLastState() == TrackerStateInitial {
		eventsInformerCleanupFn := func() {}
		if os.Getenv("KUBEDOG_DISABLE_EVENTS") != "1" {
			resourceEventsWatcher := NewResourceEventsWatcher(object, t.ResourceID, t.client, t.mapper, t.informerFactory)
			eventsInformerCleanupFn, err = resourceEventsWatcher.Run(ctx, eventCh)
			if err != nil {
				return nil, fmt.Errorf("run resource events watcher: %w", err)
			}
		}

		cleanupFn = func() {
			eventsInformerCleanupFn()
		}

		t.setStatusStabilizeAt(time.Now().Add(ResourceStatusStabilizingDuration))
		t.setLastState(TrackerStateStatusStabilizing)
		t.setLastObjectDuringStatusStabilization(object)

		return
	}

	if t.getLastState() == TrackerStateStatusStabilizing {
		t.setLastObjectDuringStatusStabilization(object)

		return
	}

	resourceStatus, err := NewResourceStatus(object)
	if err != nil {
		return nil, fmt.Errorf("construct resource status: %w", err)
	}

	switch t.getLastState() {
	case TrackerStateResourceSucceeded:
		regularCh <- resourceStatus
	case TrackerStateStarted, TrackerStateResourceAdded, TrackerStateResourceFailed:
		if resourceStatus.IsFailed() {
			t.setLastState(TrackerStateResourceFailed)
			failedCh <- resourceStatus
		} else if resourceStatus.IsReady() {
			t.setLastState(TrackerStateResourceSucceeded)
			succeededCh <- resourceStatus
		} else {
			regularCh <- resourceStatus
		}
	case TrackerStateResourceDeleted:
		if resourceStatus.IsFailed() {
			t.setLastState(TrackerStateResourceFailed)
			failedCh <- resourceStatus
		} else if resourceStatus.IsReady() {
			t.setLastState(TrackerStateResourceSucceeded)
			succeededCh <- resourceStatus
		} else {
			t.setLastState(TrackerStateResourceAdded)
			addedCh <- resourceStatus
		}
	}

	return cleanupFn, nil
}

func (t *Tracker) handleResourceDeleted(regularCh chan<- *ResourceStatus) {
	t.setLastState(TrackerStateResourceDeleted)
	regularCh <- NewDeletedResourceStatus()
}

func (t *Tracker) handleEvent(event *corev1.Event, eventCh chan<- *corev1.Event, failedCh chan<- *ResourceStatus) {
	eventStatus := NewEventStatus(event)

	if eventStatus.IsFailure() {
		t.setLastState(TrackerStateResourceFailed)
		failedCh <- NewFailedResourceStatus(eventStatus.FailureReason())
	}

	eventCh <- event
}

func (t *Tracker) lastObjectDuringStatusStabilization() *unstructured.Unstructured {
	t.lastObjDuringStatusStabilizationMux.Lock()
	defer t.lastObjDuringStatusStabilizationMux.Unlock()

	return t.lastObjDuringStatusStabilization
}

func (t *Tracker) setLastObjectDuringStatusStabilization(obj *unstructured.Unstructured) {
	t.lastObjDuringStatusStabilizationMux.Lock()
	defer t.lastObjDuringStatusStabilizationMux.Unlock()

	t.lastObjDuringStatusStabilization = obj
}

func (t *Tracker) getLastState() TrackerState {
	t.lastStateMux.Lock()
	defer t.lastStateMux.Unlock()

	return t.lastState
}

func (t *Tracker) setLastState(state TrackerState) {
	t.lastStateMux.Lock()
	defer t.lastStateMux.Unlock()

	t.lastState = state
}

func (t *Tracker) statusStabilizeAt() time.Time {
	t.statusStableAtMux.Lock()
	defer t.statusStableAtMux.Unlock()

	return t.statusStableAt
}

func (t *Tracker) setStatusStabilizeAt(at time.Time) {
	t.statusStableAtMux.Lock()
	defer t.statusStableAtMux.Unlock()

	t.statusStableAt = at
}
