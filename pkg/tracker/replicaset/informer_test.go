package replicaset

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"

	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/utils"
)

const testNamespace = "default"

// callbackFrame matches a goroutine parked inside an event callback. Matching the file
// instead of a function name keeps the check honest whether the send sits in the
// callback itself or in a helper.
const callbackFrame = "pkg/tracker/replicaset/informer.go:"

func TestRun_UnblocksEventCallbackOnCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	dep := &appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{Kind: "Deployment", APIVersion: "apps/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: testNamespace, UID: types.UID("dep-uid-1")},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "demo"}},
		},
	}
	rs := &appsv1.ReplicaSet{
		TypeMeta: metav1.TypeMeta{Kind: "ReplicaSet", APIVersion: "apps/v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo-111",
			Namespace: testNamespace,
			UID:       types.UID("rs-uid-1"),
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

	dynClient := dynamicfake.NewSimpleDynamicClient(k8sscheme.Scheme, rs)
	watchErrCh := make(chan error, 10)
	factory := informer.NewConcurrentInformerFactory(ctx.Done(), watchErrCh, dynClient, informer.ConcurrentInformerFactoryOptions{})

	rsInformer := NewReplicaSetInformer(&tracker.Tracker{
		Namespace:       testNamespace,
		ResourceName:    dep.Name,
		InformerFactory: factory,
	}, utils.ControllerAccessor(dep)).WithChannels(
		make(chan *appsv1.ReplicaSet),
		make(chan *appsv1.ReplicaSet),
		make(chan *appsv1.ReplicaSet),
		make(chan error),
	)

	cleanupFn, err := rsInformer.Run(ctx)
	if err != nil {
		t.Fatalf("run replicaset informer: %v", err)
	}

	if !waitForCallbackParked(true, 10*time.Second) {
		t.Fatal("no event callback parked on send: the test cannot tell whether cleanup releases one")
	}

	cleanupFn()

	if !waitForCallbackParked(false, 10*time.Second) {
		t.Fatal("event callback still parked 10s after cleanup: the shared informer goroutine is wedged")
	}
}

func waitForCallbackParked(want bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if callbackParked() == want {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func callbackParked() bool {
	buf := make([]byte, 64*1024)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return strings.Contains(string(buf[:n]), callbackFrame)
		}
		buf = make([]byte, 2*len(buf))
	}
}

func ptrTo[T any](v T) *T { return &v }
