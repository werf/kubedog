//go:build ai_tests

package event

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/tracker"
)

var eventsGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "events"}

// A role granting a resource but not the events of its own namespace must not stop the
// tracking; the event-based failure detection is lost, but readiness is derived from the
// resource itself.
func TestEventInformerToleratesForbiddenEvents(t *testing.T) {
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		eventsGVR: "EventList",
	})
	forbidden := func() error {
		return apierrors.NewForbidden(eventsGVR.GroupResource(), "", errors.New("no access"))
	}
	client.PrependReactor("list", eventsGVR.Resource, func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, forbidden()
	})
	client.PrependWatchReactor(eventsGVR.Resource, func(k8stesting.Action) (bool, watch.Interface, error) {
		return true, nil, forbidden()
	})

	stopCh := make(chan struct{})
	t.Cleanup(func() { close(stopCh) })

	watchErrCh := make(chan error, 10)
	nonFatalCh := make(chan error, 10)

	factory := informer.NewConcurrentInformerFactory(stopCh, watchErrCh, client, informer.ConcurrentInformerFactoryOptions{
		OnNonFatalWatchError: func(_ schema.GroupVersionResource, _ string, err error) {
			nonFatalCh <- err
		},
	})

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name:      "example",
		Namespace: "production",
		UID:       "00000000-0000-0000-0000-000000000000",
	}}
	eventInformer := NewEventInformer(&tracker.Tracker{
		Namespace:       pod.Namespace,
		ResourceName:    pod.Name,
		InformerFactory: factory,
	}, pod)

	cleanupFn, err := eventInformer.Run(context.Background())
	require.NoError(t, err, "the denied events must not stop the tracking from starting")
	t.Cleanup(cleanupFn)

	select {
	case err := <-nonFatalCh:
		assert.ErrorContains(t, err, "forbidden")
	case err := <-watchErrCh:
		t.Fatalf("the denied events stopped the tracking: %s", err)
	case <-time.After(time.Minute):
		t.Fatal("the reflector reported no error")
	}
}
