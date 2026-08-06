//go:build ai_tests

package generic

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/resid"
)

var cnpgClusterGVK = schema.GroupVersionKind{Group: "postgresql.cnpg.io", Version: "v1", Kind: "Cluster"}

var cnpgClusterGVR = schema.GroupVersionResource{Group: "postgresql.cnpg.io", Version: "v1", Resource: "clusters"}

func cnpgCluster(conditionStatus string) *unstructured.Unstructured {
	object := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "postgresql.cnpg.io/v1",
		"kind":       "Cluster",
		"metadata": map[string]interface{}{
			"name":      "test-pg",
			"namespace": "default",
			"uid":       "test-pg-uid",
		},
	}}

	if conditionStatus != "" {
		object.Object["status"] = map[string]interface{}{
			"conditions": []interface{}{
				map[string]interface{}{"type": "Ready", "status": conditionStatus},
			},
		}
	}

	return object
}

// The customer-reported scenario: the resource is created before its controller
// publishes any status, and the Ready condition only appears later. The tracker
// must stay pending across that window and succeed only on Ready=True.
func TestAI_TrackerWaitsForLateCNPGReadyCondition(t *testing.T) {
	t.Setenv("KUBEDOG_DISABLE_EVENTS", "1")

	scheme := runtime.NewScheme()
	initial := cnpgCluster("")
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{cnpgClusterGVR: "ClusterList"}, initial)

	mapper := meta.NewDefaultRESTMapper(nil)
	mapper.AddSpecific(cnpgClusterGVK, cnpgClusterGVR,
		schema.GroupVersionResource{Group: cnpgClusterGVR.Group, Version: cnpgClusterGVR.Version, Resource: "cluster"},
		meta.RESTScopeNamespace)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	stopCh := make(chan struct{})
	defer close(stopCh)
	watchErrCh := make(chan error, 1)

	informerFactory := informer.NewConcurrentInformerFactory(stopCh, watchErrCh, client, informer.ConcurrentInformerFactoryOptions{})

	tracker := NewTracker(
		resid.NewResourceID("test-pg", cnpgClusterGVK, resid.NewResourceIDOptions{Namespace: "default"}),
		client, nil, informerFactory, mapper,
	)

	addedCh := make(chan *ResourceStatus, 64)
	succeededCh := make(chan *ResourceStatus, 64)
	failedCh := make(chan *ResourceStatus, 64)
	regularCh := make(chan *ResourceStatus, 64)
	eventCh := make(chan *corev1.Event, 64)

	trackDoneCh := make(chan error, 1)
	go func() {
		trackDoneCh <- tracker.Track(ctx, 30*time.Second, addedCh, succeededCh, failedCh, regularCh, eventCh)
	}()

	// Wait past the stabilization window: a pending verdict must be produced,
	// and nothing may land on succeededCh even though no condition exists yet.
	select {
	case status := <-regularCh:
		assert.False(t, status.IsReady(), "a Cluster with no status must not be ready")
	case status := <-succeededCh:
		t.Fatalf("tracker reported success before any Ready condition existed (ready=%v)", status.IsReady())
	case <-time.After(20 * time.Second):
		t.Fatal("no status verdict produced within the stabilization window")
	}

	updateCluster(t, client, "False")
	requireNoSuccess(t, succeededCh, 5*time.Second)

	updateCluster(t, client, "True")

	select {
	case status := <-succeededCh:
		assert.True(t, status.IsReady())
		assert.Equal(t, "status.conditions[type=Ready].status", status.HumanConditionPath())
	case <-time.After(20 * time.Second):
		t.Fatal("tracker never reported success after Ready=True")
	}

	cancel()
	select {
	case <-trackDoneCh:
	case <-time.After(10 * time.Second):
		t.Fatal("Track did not return after context cancellation")
	}
}

func updateCluster(t *testing.T, client *dynamicfake.FakeDynamicClient, conditionStatus string) {
	t.Helper()

	object := cnpgCluster(conditionStatus)
	_, err := client.Resource(cnpgClusterGVR).Namespace("default").Update(context.Background(), object, metav1.UpdateOptions{})
	require.NoError(t, err)
}

func requireNoSuccess(t *testing.T, succeededCh <-chan *ResourceStatus, wait time.Duration) {
	t.Helper()

	select {
	case status := <-succeededCh:
		t.Fatalf("tracker reported success while Ready=False (ready=%v)", status.IsReady())
	case <-time.After(wait):
	}
}
