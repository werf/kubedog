package dyntracker

import (
	"context"
	"fmt"
	"io"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"

	"github.com/werf/kubedog/pkg/trackers/dyntracker/statestore"
	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
)

type DynamicPresenceTracker struct {
	taskState     *util.Concurrent[*statestore.PresenceTaskState]
	dynamicClient dynamic.Interface
	mapper        meta.ResettableRESTMapper

	timeout    time.Duration
	pollPeriod time.Duration
}

func NewDynamicPresenceTracker(
	taskState *util.Concurrent[*statestore.PresenceTaskState],
	dynamicClient dynamic.Interface,
	mapper meta.ResettableRESTMapper,
	opts DynamicPresenceTrackerOptions,
) *DynamicPresenceTracker {
	var timeout time.Duration
	if opts.Timeout != 0 {
		timeout = opts.Timeout
	} else {
		timeout = 5 * time.Minute
	}

	var pollPeriod time.Duration
	if opts.PollPeriod != 0 {
		pollPeriod = opts.PollPeriod
	} else {
		pollPeriod = 1 * time.Second
	}

	return &DynamicPresenceTracker{
		taskState:     taskState,
		dynamicClient: dynamicClient,
		mapper:        mapper,
		timeout:       timeout,
		pollPeriod:    pollPeriod,
	}
}

type DynamicPresenceTrackerOptions struct {
	Timeout    time.Duration
	PollPeriod time.Duration
}

func (t *DynamicPresenceTracker) Track(ctx context.Context) error {
	var (
		name             string
		namespace        string
		groupVersionKind schema.GroupVersionKind
	)
	t.taskState.RTransaction(func(ts *statestore.PresenceTaskState) {
		name = ts.Name()
		namespace = ts.Namespace()
		groupVersionKind = ts.GroupVersionKind()
	})

	namespaced, err := util.IsNamespaced(groupVersionKind, t.mapper)
	if err != nil {
		return fmt.Errorf("check if namespaced: %w", err)
	}

	gvr, err := util.GVRFromGVK(groupVersionKind, t.mapper)
	if err != nil {
		return fmt.Errorf("get GroupVersionResource: %w", err)
	}

	var resourceClient dynamic.ResourceInterface
	if namespaced {
		resourceClient = t.dynamicClient.Resource(gvr).Namespace(namespace)
	} else {
		resourceClient = t.dynamicClient.Resource(gvr)
	}

	resourceHumanID := util.ResourceHumanID(name, namespace, groupVersionKind, t.mapper)

	if err := wait.PollImmediate(t.pollPeriod, t.timeout, func() (bool, error) {
		if _, err := resourceClient.Get(ctx, name, metav1.GetOptions{}); err != nil {
			if apierrors.IsResourceExpired(err) || apierrors.IsGone(err) || err == io.EOF || err == io.ErrUnexpectedEOF {
				return false, nil
			}

			if apierrors.IsNotFound(err) {
				t.taskState.RWTransaction(func(pts *statestore.PresenceTaskState) {
					pts.ResourceState().RWTransaction(func(rs *statestore.ResourceState) {
						rs.SetStatus(statestore.ResourceStatusDeleted)
					})
				})

				return false, nil
			}

			return false, fmt.Errorf("get resource %q: %w", resourceHumanID, err)
		}

		return true, nil
	}); err != nil {
		return fmt.Errorf("poll resource %q: %w", resourceHumanID, err)
	}

	t.taskState.RWTransaction(func(pts *statestore.PresenceTaskState) {
		pts.ResourceState().RWTransaction(func(rs *statestore.ResourceState) {
			rs.SetStatus(statestore.ResourceStatusCreated)
		})
	})

	return nil
}
