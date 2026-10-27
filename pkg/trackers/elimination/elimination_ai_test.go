//go:build ai_tests

package elimination

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

var configMapsGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}

// optionsRecordingClient records the options the reflector passes to List and Watch, which
// the fake dynamic client drops. Like the API server, it ends a watch-list stream with the
// initial-events-end bookmark only when initial events were requested.
type optionsRecordingClient struct {
	dynamic.Interface

	mu           sync.Mutex
	listOptions  []metav1.ListOptions
	watchOptions []metav1.ListOptions
}

func (c *optionsRecordingClient) Resource(gvr schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return &optionsRecordingResource{NamespaceableResourceInterface: c.Interface.Resource(gvr), client: c}
}

func (c *optionsRecordingClient) recorded() ([]metav1.ListOptions, []metav1.ListOptions) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]metav1.ListOptions(nil), c.listOptions...), append([]metav1.ListOptions(nil), c.watchOptions...)
}

type optionsRecordingResource struct {
	dynamic.NamespaceableResourceInterface
	client *optionsRecordingClient
}

func (r *optionsRecordingResource) Namespace(namespace string) dynamic.ResourceInterface {
	return &optionsRecordingNamespacedResource{ResourceInterface: r.NamespaceableResourceInterface.Namespace(namespace), client: r.client}
}

type optionsRecordingNamespacedResource struct {
	dynamic.ResourceInterface
	client *optionsRecordingClient
}

func (r *optionsRecordingNamespacedResource) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	r.client.mu.Lock()
	r.client.listOptions = append(r.client.listOptions, opts)
	r.client.mu.Unlock()

	return r.ResourceInterface.List(ctx, opts)
}

func (r *optionsRecordingNamespacedResource) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	r.client.mu.Lock()
	r.client.watchOptions = append(r.client.watchOptions, opts)
	r.client.mu.Unlock()

	if opts.SendInitialEvents != nil && *opts.SendInitialEvents {
		bookmark := &unstructured.Unstructured{}
		bookmark.SetResourceVersion("1")
		bookmark.SetAnnotations(map[string]string{"k8s.io/initial-events-end": "true"})

		w := watch.NewFakeWithChanSize(1, false)
		w.Action(watch.Bookmark, bookmark)
		return w, nil
	}

	return r.ResourceInterface.Watch(ctx, opts)
}

func trackAbsentConfigMap(t *testing.T) *optionsRecordingClient {
	t.Helper()

	client := &optionsRecordingClient{
		Interface: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
			configMapsGVR: "ConfigMapList",
		}),
	}

	err := TrackUntilEliminated(context.Background(), client, []*EliminationTrackerSpec{{
		ResourceName:         "example",
		Namespace:            "production",
		GroupVersionResource: configMapsGVR,
	}}, EliminationTrackerOptions{Timeout: 10 * time.Second})
	require.NoError(t, err, "tracking of an absent resource must finish before the timeout")

	return client
}

func TestTrackUntilEliminatedPreservesListOptions(t *testing.T) {
	t.Setenv("ENABLE_CLIENT_GO_WATCH_LIST_ALPHA", "")
	t.Setenv("KUBE_FEATURE_WatchListClient", "false")

	listOptions, _ := trackAbsentConfigMap(t).recorded()

	require.NotEmpty(t, listOptions)
	assert.Equal(t, "metadata.name=example", listOptions[0].FieldSelector)
	assert.Equal(t, "0", listOptions[0].ResourceVersion, "the reflector's resource version must reach the client")
}

// The env var name enabling the watch-list reflector differs between client-go versions.
func TestTrackUntilEliminatedPreservesWatchListOptions(t *testing.T) {
	t.Setenv("ENABLE_CLIENT_GO_WATCH_LIST_ALPHA", "true")
	t.Setenv("KUBE_FEATURE_WatchListClient", "true")

	_, watchOptions := trackAbsentConfigMap(t).recorded()

	require.NotEmpty(t, watchOptions)
	opts := watchOptions[0]
	assert.Equal(t, "metadata.name=example", opts.FieldSelector)
	assert.True(t, opts.AllowWatchBookmarks)
	assert.NotNil(t, opts.TimeoutSeconds)
	if assert.NotNil(t, opts.SendInitialEvents) {
		assert.True(t, *opts.SendInitialEvents)
	}
	assert.Equal(t, metav1.ResourceVersionMatchNotOlderThan, opts.ResourceVersionMatch)
}
