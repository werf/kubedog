package replicaset

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"

	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/utils"
)

var lifecycleReplicaSetGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}

type lifecycleInformerHarness struct {
	ctx        context.Context
	dynamic    dynamic.Interface
	added      chan *appsv1.ReplicaSet
	modified   chan *appsv1.ReplicaSet
	deleted    chan *appsv1.ReplicaSet
	unselected chan *appsv1.ReplicaSet
	synced     chan struct{}
}

func TestRun_whenInitialReplicaSetsAreConsumed(t *testing.T) {
	replicaSet := lifecycleReplicaSet("demo-111", "rs-uid-1")
	h := newLifecycleInformerHarness(t, replicaSet)

	select {
	case <-h.synced:
		t.Fatal("handler reported synced before its initial Add was consumed")
	default:
	}

	receiveReplicaSet(t, h.added)
	select {
	case <-h.synced:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for handler sync")
	}
}

func TestRun_whenUpdateReplacesReplicaSetUID(t *testing.T) {
	oldReplicaSet := lifecycleReplicaSet("demo-111", "rs-uid-old")
	h := newLifecycleInformerHarness(t, oldReplicaSet)

	if got := receiveReplicaSet(t, h.added); got.UID != oldReplicaSet.UID {
		t.Fatalf("initial Added UID = %q, want %q", got.UID, oldReplicaSet.UID)
	}

	newReplicaSet := oldReplicaSet.DeepCopy()
	newReplicaSet.UID = types.UID("rs-uid-new")
	updateLifecycleReplicaSet(t, h, newReplicaSet)

	if got := receiveReplicaSet(t, h.deleted); got.UID != oldReplicaSet.UID {
		t.Fatalf("replacement Deleted UID = %q, want %q", got.UID, oldReplicaSet.UID)
	}
	if got := receiveReplicaSet(t, h.added); got.UID != newReplicaSet.UID {
		t.Fatalf("replacement Added UID = %q, want %q", got.UID, newReplicaSet.UID)
	}
}

func TestRun_whenReplacementStopsMatchingSelector(t *testing.T) {
	oldReplicaSet := lifecycleReplicaSet("demo-111", "rs-uid-old")
	h := newLifecycleInformerHarness(t, oldReplicaSet)

	if got := receiveReplicaSet(t, h.added); got.UID != oldReplicaSet.UID {
		t.Fatalf("initial Added UID = %q, want %q", got.UID, oldReplicaSet.UID)
	}

	replacement := oldReplicaSet.DeepCopy()
	replacement.UID = types.UID("rs-uid-new")
	replacement.Labels = map[string]string{"app": "other"}
	updateLifecycleReplicaSet(t, h, replacement)

	if got := receiveReplicaSet(t, h.deleted); got.UID != oldReplicaSet.UID {
		t.Fatalf("replacement Deleted UID = %q, want %q", got.UID, oldReplicaSet.UID)
	}
}

func TestRun_whenReplicaSetLeavesAndReentersSelector(t *testing.T) {
	replicaSet := lifecycleReplicaSet("demo-111", "rs-uid-1")
	h := newLifecycleInformerHarness(t, replicaSet)

	if got := receiveReplicaSet(t, h.added); got.UID != replicaSet.UID {
		t.Fatalf("initial Added UID = %q, want %q", got.UID, replicaSet.UID)
	}

	unselected := replicaSet.DeepCopy()
	unselected.Labels = map[string]string{"app": "other"}
	updateLifecycleReplicaSet(t, h, unselected)
	if got := receiveReplicaSet(t, h.unselected); got.UID != replicaSet.UID {
		t.Fatalf("Unselected UID = %q, want %q", got.UID, replicaSet.UID)
	}

	reentered := replicaSet.DeepCopy()
	updateLifecycleReplicaSet(t, h, reentered)
	if got := receiveReplicaSet(t, h.added); got.UID != replicaSet.UID {
		t.Fatalf("reentry Added UID = %q, want %q", got.UID, replicaSet.UID)
	}
}

func newLifecycleInformerHarness(t *testing.T, seed *appsv1.ReplicaSet) *lifecycleInformerHarness {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	dynClient := dynamicfake.NewSimpleDynamicClient(k8sscheme.Scheme, seed)
	watchErrCh := make(chan error, 10)
	factory := informer.NewConcurrentInformerFactory(ctx.Done(), watchErrCh, dynClient, informer.ConcurrentInformerFactoryOptions{})
	dep := lifecycleDeployment()

	h := &lifecycleInformerHarness{
		ctx:        ctx,
		dynamic:    dynClient,
		added:      make(chan *appsv1.ReplicaSet),
		modified:   make(chan *appsv1.ReplicaSet),
		deleted:    make(chan *appsv1.ReplicaSet),
		unselected: make(chan *appsv1.ReplicaSet),
		synced:     make(chan struct{}),
	}

	rsInformer := NewReplicaSetInformer(&tracker.Tracker{
		Namespace:       testNamespace,
		ResourceName:    dep.Name,
		InformerFactory: factory,
	}, utils.ControllerAccessor(dep)).WithChannels(h.added, h.modified, h.deleted, watchErrCh).
		WithUnselectedChannel(h.unselected).
		WithSyncedChannel(h.synced)

	cleanup, err := rsInformer.Run(ctx)
	if err != nil {
		t.Fatalf("run ReplicaSet informer: %v", err)
	}
	t.Cleanup(cleanup)

	return h
}

func updateLifecycleReplicaSet(t *testing.T, h *lifecycleInformerHarness, rs *appsv1.ReplicaSet) {
	t.Helper()

	current, err := h.dynamic.Resource(lifecycleReplicaSetGVR).Namespace(testNamespace).Get(h.ctx, rs.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get ReplicaSet: %v", err)
	}
	rs.ResourceVersion = current.GetResourceVersion()

	object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(rs)
	if err != nil {
		t.Fatalf("convert ReplicaSet: %v", err)
	}
	if _, err := h.dynamic.Resource(lifecycleReplicaSetGVR).Namespace(testNamespace).Update(
		h.ctx,
		&unstructured.Unstructured{Object: object},
		metav1.UpdateOptions{},
	); err != nil {
		t.Fatalf("update ReplicaSet: %v", err)
	}
}

func receiveReplicaSet(t *testing.T, ch <-chan *appsv1.ReplicaSet) *appsv1.ReplicaSet {
	t.Helper()

	select {
	case rs := <-ch:
		return rs
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for ReplicaSet event")
		return nil
	}
}

func lifecycleDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{Kind: "Deployment", APIVersion: "apps/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: testNamespace, UID: types.UID("dep-uid-1")},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "demo"}},
		},
	}
}

func lifecycleReplicaSet(name string, uid types.UID) *appsv1.ReplicaSet {
	dep := lifecycleDeployment()
	return &appsv1.ReplicaSet{
		TypeMeta: metav1.TypeMeta{Kind: "ReplicaSet", APIVersion: "apps/v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			UID:       uid,
			Labels:    map[string]string{"app": "demo"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "Deployment", Name: dep.Name, UID: dep.UID, Controller: ptrTo(true),
			}},
		},
		Spec: appsv1.ReplicaSetSpec{
			Selector: dep.Spec.Selector,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "demo"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "busybox:1"}}},
			},
		},
	}
}
