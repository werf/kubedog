//go:build ai_tests

package deployment

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/werf/kubedog/pkg/informer"
	legacy "github.com/werf/kubedog/pkg/tracker/deployment"
)

const testNamespace = "default"

var testPodTemplate = corev1.PodTemplateSpec{
	ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}},
	Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "web", Image: "nginx:1"}}},
}

func testDeployment(status appsv1.DeploymentStatus) *appsv1.Deployment {
	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{
			Name:       "web",
			Namespace:  testNamespace,
			UID:        "web-uid",
			Generation: 1,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(2),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			Template: testPodTemplate,
		},
		Status: status,
	}
}

func testReplicaSet(name string, template corev1.PodTemplateSpec) *appsv1.ReplicaSet {
	return &appsv1.ReplicaSet{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "ReplicaSet"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			Labels:    map[string]string{"app": "web"},
		},
		Spec: appsv1.ReplicaSetSpec{Template: template},
	}
}

func toUnstructured(t *testing.T, obj runtime.Object) *unstructured.Unstructured {
	t.Helper()

	content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	require.NoError(t, err)

	return &unstructured.Unstructured{Object: content}
}

type testEnv struct {
	client  *dynamicfake.FakeDynamicClient
	factory func() *Tracker
}

func newTestEnv(t *testing.T, objects ...runtime.Object) *testEnv {
	t.Helper()
	t.Setenv("KUBEDOG_DISABLE_EVENTS", "1")

	unstructuredObjects := make([]runtime.Object, 0, len(objects))
	for _, obj := range objects {
		unstructuredObjects = append(unstructuredObjects, toUnstructured(t, obj))
	}

	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		deploymentGVR: "DeploymentList",
		replicaSetGVR: "ReplicaSetList",
		podGVR:        "PodList",
	}, unstructuredObjects...)

	stopCh := make(chan struct{})
	t.Cleanup(func() { close(stopCh) })

	informerFactory := informer.NewConcurrentInformerFactory(stopCh, make(chan error, 1), client, informer.ConcurrentInformerFactoryOptions{})

	return &testEnv{
		client: client,
		factory: func() *Tracker {
			return NewTracker("web", testNamespace, nil, informerFactory, Options{})
		},
	}
}

func (e *testEnv) updateDeployment(t *testing.T, object *appsv1.Deployment) {
	t.Helper()

	_, err := e.client.Resource(deploymentGVR).Namespace(testNamespace).Update(context.Background(), toUnstructured(t, object), metav1.UpdateOptions{})
	require.NoError(t, err)
}

func runTracker(t *testing.T, trk *Tracker) (context.CancelFunc, <-chan error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() { errCh <- trk.Track(ctx) }()

	return cancel, errCh
}

// nextEvent returns the next event of the given type, skipping the events of the other types.
func nextEvent[T Event](t *testing.T, trk *Tracker) T {
	t.Helper()

	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev := <-trk.Events():
			if typed, ok := ev.(T); ok {
				return typed
			}
		case <-timeout:
			var zero T
			t.Fatalf("timed out waiting for %T", zero)
		}
	}
}

func TestAI_TrackerReportsAddedThenReady(t *testing.T) {
	env := newTestEnv(t, testDeployment(appsv1.DeploymentStatus{ObservedGeneration: 1}))
	trk := env.factory()
	runTracker(t, trk)

	added := nextEvent[AddedEvent](t, trk)
	assert.False(t, added.Status.Ready)
	assert.Equal(t, []string{"up-to-date 0->2", "replicas 0->2", "available 0->2"}, added.Status.WaitingFor)
	require.NotNil(t, added.Status.Object)
	assert.Equal(t, "web", added.Status.Object.Name)

	env.updateDeployment(t, testDeployment(appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 2, UpdatedReplicas: 2, AvailableReplicas: 2}))

	ready := nextEvent[ReadyEvent](t, trk)
	assert.True(t, ready.Status.Ready)
	assert.Empty(t, ready.Status.WaitingFor)
	assert.Greater(t, ready.Status.Generation, added.Status.Generation)
}

func TestAI_TrackerReportsStatusWhileProgressing(t *testing.T) {
	env := newTestEnv(t, testDeployment(appsv1.DeploymentStatus{ObservedGeneration: 1}))
	trk := env.factory()
	runTracker(t, trk)

	nextEvent[AddedEvent](t, trk)

	env.updateDeployment(t, testDeployment(appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 2, UpdatedReplicas: 2, AvailableReplicas: 1}))

	status := nextEvent[StatusEvent](t, trk)
	assert.False(t, status.Status.Ready)
	assert.Equal(t, []string{"available 1->2"}, status.Status.WaitingFor)
}

func TestAI_TrackerReportsDeletion(t *testing.T) {
	env := newTestEnv(t, testDeployment(appsv1.DeploymentStatus{ObservedGeneration: 1}))
	trk := env.factory()
	runTracker(t, trk)

	nextEvent[AddedEvent](t, trk)

	require.NoError(t, env.client.Resource(deploymentGVR).Namespace(testNamespace).Delete(context.Background(), "web", metav1.DeleteOptions{}))

	nextEvent[DeletedEvent](t, trk)
}

func TestAI_TrackerTellsNewReplicaSetFromOld(t *testing.T) {
	oldTemplate := *testPodTemplate.DeepCopy()
	oldTemplate.Spec.Containers[0].Image = "nginx:0"

	env := newTestEnv(t,
		testDeployment(appsv1.DeploymentStatus{ObservedGeneration: 1}),
		testReplicaSet("web-old", oldTemplate),
		testReplicaSet("web-new", testPodTemplate),
	)
	trk := env.factory()
	runTracker(t, trk)

	isNew := map[string]bool{}
	for range 2 {
		ev := nextEvent[ReplicaSetAddedEvent](t, trk)
		isNew[ev.ReplicaSet.Name] = ev.ReplicaSet.IsNew
	}

	assert.Equal(t, map[string]bool{"web-old": false, "web-new": true}, isNew)
}

func TestAI_TrackerStopsOnContextCancel(t *testing.T) {
	env := newTestEnv(t, testDeployment(appsv1.DeploymentStatus{ObservedGeneration: 1}))
	trk := env.factory()
	cancel, errCh := runTracker(t, trk)

	nextEvent[AddedEvent](t, trk)
	cancel()

	select {
	case err := <-errCh:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(10 * time.Second):
		t.Fatal("tracker did not stop")
	}
}

func TestAI_LegacyTrackerFeedsLegacyChannels(t *testing.T) {
	env := newTestEnv(t, testDeployment(appsv1.DeploymentStatus{ObservedGeneration: 1}))
	legacyTracker := &LegacyTracker{tracker: env.factory()}
	legacyTracker.Feed = legacy.NewFeed()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() { _ = legacyTracker.Track(ctx) }()

	select {
	case status := <-legacyTracker.Added:
		assert.False(t, status.IsReady)
		require.NotNil(t, status.ReplicasIndicator)
		assert.Equal(t, int32(2), status.ReplicasIndicator.TargetValue)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for Added")
	}

	env.updateDeployment(t, testDeployment(appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 2, UpdatedReplicas: 2, AvailableReplicas: 2}))

	select {
	case status := <-legacyTracker.Ready:
		assert.True(t, status.IsReady)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for Ready")
	}
}

func TestAI_LegacyTrackerStopsWhenNobodyReads(t *testing.T) {
	env := newTestEnv(t, testDeployment(appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 2, UpdatedReplicas: 2, AvailableReplicas: 2}))
	legacyTracker := &LegacyTracker{tracker: env.factory()}
	legacyTracker.Feed = legacy.NewFeed()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- legacyTracker.Track(ctx) }()

	// Ready is unbuffered and never read: canceling must still unblock the tracker.
	time.Sleep(500 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(10 * time.Second):
		t.Fatal("tracker did not stop")
	}
}

func TestAI_TrackerReportsPodOfNewReplicaSet(t *testing.T) {
	oldTemplate := *testPodTemplate.DeepCopy()
	oldTemplate.Spec.Containers[0].Image = "nginx:0"

	newPod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name:            "web-new-abc",
			Namespace:       testNamespace,
			Labels:          map[string]string{"app": "web"},
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "web-new", UID: "web-new-uid"}},
		},
		Spec: testPodTemplate.Spec,
	}

	env := newTestEnv(t,
		testDeployment(appsv1.DeploymentStatus{ObservedGeneration: 1}),
		testReplicaSet("web-old", oldTemplate),
		testReplicaSet("web-new", testPodTemplate),
		newPod,
	)
	trk := env.factory()
	runTracker(t, trk)

	// Pods are watched only after all the ReplicaSets are known, so the Pod is told to
	// belong to the new ReplicaSet even though both ReplicaSets exist.
	ev := nextEvent[PodAddedEvent](t, trk)
	assert.Equal(t, "web-new-abc", ev.PodName)
	assert.Equal(t, ReplicaSet{Name: "web-new", IsNew: true}, ev.ReplicaSet)
}
