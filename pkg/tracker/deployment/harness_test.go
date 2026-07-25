package deployment_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
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
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"

	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/tracker/deployment"
)

const (
	testNamespace      = "default"
	testDeploymentName = "demo"
)

var (
	gvrDeployments = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	gvrReplicaSets = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}

	depUID = types.UID("dep-uid-1")

	errTimeout   = errors.New("timeout")
	errTrackDone = errors.New("Track() exited")
)

func ptrTo[T any](v T) *T { return &v }

type observation struct {
	Kind string
	Data any
}

type harnessConfig struct {
	dynamicSeed []runtime.Object
}

type harness struct {
	ctx     context.Context
	dynamic dynamic.Interface
	tracker *deployment.Tracker

	events  chan observation
	backlog []observation

	done       chan struct{}
	wg         sync.WaitGroup
	trackErrMu sync.Mutex
	trackErr   error
}

func newHarness(t *testing.T, cfg harnessConfig) *harness {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())

	dynClient := dynamicfake.NewSimpleDynamicClient(k8sscheme.Scheme, cfg.dynamicSeed...)
	kubeClient := k8sfake.NewSimpleClientset()

	watchErrCh := make(chan error, 100)
	factory := informer.NewConcurrentInformerFactory(ctx.Done(), watchErrCh, dynClient, informer.ConcurrentInformerFactoryOptions{})

	trk := deployment.NewTracker(testDeploymentName, testNamespace, kubeClient, factory, tracker.Options{IgnoreLogs: true})

	h := &harness{
		ctx:     ctx,
		dynamic: dynClient,
		tracker: trk,
		events:  make(chan observation, 4096),
		done:    make(chan struct{}),
	}

	forwardChan(h, "Added", trk.Added)
	forwardChan(h, "Ready", trk.Ready)
	forwardChan(h, "Failed", trk.Failed)
	forwardChan(h, "Status", trk.Status)
	forwardChan(h, "EventMsg", trk.EventMsg)
	forwardChan(h, "AddedReplicaSet", trk.AddedReplicaSet)
	forwardChan(h, "AddedPod", trk.AddedPod)
	forwardChan(h, "PodError", trk.PodError)
	forwardChan(h, "WatchErr", watchErrCh)
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		for {
			select {
			case <-trk.PodLogChunk:
			case <-h.ctx.Done():
				return
			}
		}
	}()

	go func() {
		defer close(h.done)
		defer func() {
			if r := recover(); r != nil {
				h.setTrackErr(fmt.Errorf("panic in Track(): %v", r))
			}
		}()
		h.setTrackErr(trk.Track(ctx))
	}()

	t.Cleanup(func() {
		cancel()
		select {
		case <-h.done:
		case <-time.After(10 * time.Second):
			t.Error("Track() did not exit within 10s after cancel")
		}

		forwardersDone := make(chan struct{})
		go func() {
			h.wg.Wait()
			close(forwardersDone)
		}()
		select {
		case <-forwardersDone:
		case <-time.After(5 * time.Second):
			t.Error("forwarder goroutines did not exit within 5s after cancel")
		}
	})

	return h
}

func forwardChan[T any](h *harness, kind string, ch <-chan T) {
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		for {
			select {
			case v := <-ch:
				select {
				case h.events <- observation{kind, v}:
				case <-h.ctx.Done():
					return
				}
			case <-h.ctx.Done():
				return
			}
		}
	}()
}

func (h *harness) setTrackErr(err error) {
	h.trackErrMu.Lock()
	defer h.trackErrMu.Unlock()
	if h.trackErr == nil {
		h.trackErr = err
	}
}

func (h *harness) getTrackErr() error {
	h.trackErrMu.Lock()
	defer h.trackErrMu.Unlock()
	return h.trackErr
}

func (h *harness) waitFor(kind string, timeout time.Duration, pred func(observation) bool) (observation, error) {
	for i, ev := range h.backlog {
		if ev.Kind == kind && (pred == nil || pred(ev)) {
			h.backlog = append(h.backlog[:i:i], h.backlog[i+1:]...)
			return ev, nil
		}
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case ev := <-h.events:
			if ev.Kind == kind && (pred == nil || pred(ev)) {
				return ev, nil
			}
			h.backlog = append(h.backlog, ev)
		case <-h.done:
			return observation{}, fmt.Errorf("%w (err=%v) while waiting for kind=%s", errTrackDone, h.getTrackErr(), kind)
		case <-deadline.C:
			return observation{}, fmt.Errorf("%w after %s waiting for kind=%s", errTimeout, timeout, kind)
		}
	}
}

func (h *harness) waitForNone(kind string, quiet time.Duration, pred func(observation) bool) error {
	ev, err := h.waitFor(kind, quiet, pred)
	if err == nil {
		return fmt.Errorf("expected no %s within %s, but got: %+v", kind, quiet, ev)
	}
	if errors.Is(err, errTimeout) {
		return nil
	}
	return err
}

func mustToUnstructured(t *testing.T, obj runtime.Object) *unstructured.Unstructured {
	t.Helper()
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		t.Fatalf("to unstructured: %v", err)
	}
	return &unstructured.Unstructured{Object: m}
}

func (h *harness) createObject(t *testing.T, gvr schema.GroupVersionResource, obj runtime.Object) {
	t.Helper()
	if _, err := h.dynamic.Resource(gvr).Namespace(testNamespace).Create(h.ctx, mustToUnstructured(t, obj), metav1.CreateOptions{}); err != nil {
		t.Fatalf("create %s: %v", gvr.Resource, err)
	}
}

func (h *harness) updateReplicaSet(t *testing.T, rs *appsv1.ReplicaSet) {
	t.Helper()
	cur, err := h.dynamic.Resource(gvrReplicaSets).Namespace(testNamespace).Get(h.ctx, rs.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get current replicaset: %v", err)
	}
	rs.ResourceVersion = cur.GetResourceVersion()
	if _, err := h.dynamic.Resource(gvrReplicaSets).Namespace(testNamespace).Update(h.ctx, mustToUnstructured(t, rs), metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update replicaset: %v", err)
	}
}

func (h *harness) updateDeployment(t *testing.T, dep *appsv1.Deployment) {
	t.Helper()
	cur, err := h.dynamic.Resource(gvrDeployments).Namespace(testNamespace).Get(h.ctx, dep.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get current deployment: %v", err)
	}
	dep.ResourceVersion = cur.GetResourceVersion()
	if _, err := h.dynamic.Resource(gvrDeployments).Namespace(testNamespace).Update(h.ctx, mustToUnstructured(t, dep), metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update deployment: %v", err)
	}
}

func (h *harness) deleteObject(t *testing.T, gvr schema.GroupVersionResource, name string) {
	t.Helper()
	if err := h.dynamic.Resource(gvr).Namespace(testNamespace).Delete(h.ctx, name, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete %s/%s: %v", gvr.Resource, name, err)
	}
}

func baseSelector() *metav1.LabelSelector {
	return &metav1.LabelSelector{MatchLabels: map[string]string{"app": "demo"}}
}

func basePodTemplate() corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "demo"}},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "c", Image: "busybox:1"}},
		},
	}
}

func newNotReadyDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{Kind: "Deployment", APIVersion: "apps/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: testDeploymentName, Namespace: testNamespace, UID: depUID, Generation: 1},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptrTo(int32(1)),
			Selector: baseSelector(),
			Template: basePodTemplate(),
		},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 1},
	}
}

func newReplicaSet(name string, uid types.UID) *appsv1.ReplicaSet {
	return &appsv1.ReplicaSet{
		TypeMeta: metav1.TypeMeta{Kind: "ReplicaSet", APIVersion: "apps/v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			UID:       uid,
			Labels:    map[string]string{"app": "demo"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "Deployment", Name: testDeploymentName, UID: depUID, Controller: ptrTo(true),
			}},
			CreationTimestamp: metav1.Now(),
		},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: ptrTo(int32(1)),
			Selector: baseSelector(),
			Template: basePodTemplate(),
		},
	}
}

func withReplicaFailure(rs *appsv1.ReplicaSet, reason, marker string) *appsv1.ReplicaSet {
	rs.Status.Conditions = []appsv1.ReplicaSetCondition{{
		Type:               appsv1.ReplicaSetReplicaFailure,
		Status:             corev1.ConditionTrue,
		Reason:             reason,
		Message:            fmt.Sprintf("pods %q is forbidden: exceeded quota: compute-quota [%s]", rs.Name, marker),
		LastTransitionTime: metav1.Now(),
	}}
	return rs
}

func failedWithMarker(marker string) func(observation) bool {
	return func(ev observation) bool {
		status, ok := ev.Data.(deployment.DeploymentStatus)
		if !ok {
			return false
		}
		return status.IsFailed && strings.Contains(status.FailedReason, marker)
	}
}
