//go:build ai_tests

package generic

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/werf/kubedog/pkg/dyntracker/util"
	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/resid"
)

var eventsGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "events"}

// newForbiddenEventsFactory returns a factory over a client denying both list and watch of
// events, along with the channel the fatal errors are reported to and the channel the
// non-fatal ones are reported to.
func newForbiddenEventsFactory(t *testing.T) (*util.Concurrent[*informer.InformerFactory], chan error, chan error) {
	t.Helper()

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

	return factory, watchErrCh, nonFatalCh
}

// Events of a cluster-scoped resource live in the "default" namespace, which the user
// allowed to read the resource itself is not necessarily allowed to read.
func TestResourceEventsWatcherToleratesForbiddenEventsOfClusterScopedResource(t *testing.T) {
	assertEventsFeedIsLostButTolerated(t, meta.RESTScopeRoot, "")
}

func TestResourceEventsWatcherToleratesForbiddenEventsOfNamespacedResource(t *testing.T) {
	assertEventsFeedIsLostButTolerated(t, meta.RESTScopeNamespace, "production")
}

func assertEventsFeedIsLostButTolerated(t *testing.T, scope meta.RESTScope, namespace string) {
	t.Helper()

	factory, watchErrCh, nonFatalCh := newForbiddenEventsFactory(t)

	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Example"}
	mapper := meta.NewDefaultRESTMapper(nil)
	mapper.Add(gvk, scope)

	object := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": gvk.GroupVersion().String(),
		"kind":       gvk.Kind,
		"metadata": map[string]interface{}{
			"name":      "example",
			"namespace": namespace,
			"uid":       "00000000-0000-0000-0000-000000000000",
		},
	}}
	resID := resid.NewResourceID("example", gvk, resid.NewResourceIDOptions{Namespace: namespace})

	watcher := NewResourceEventsWatcher(object, resID, mapper, factory)

	cleanupFn, err := watcher.Run(context.Background(), make(chan *corev1.Event, 10))
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
