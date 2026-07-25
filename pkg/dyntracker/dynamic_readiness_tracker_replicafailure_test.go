package dyntracker_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"

	"github.com/werf/kubedog/pkg/dyntracker"
	"github.com/werf/kubedog/pkg/dyntracker/logstore"
	"github.com/werf/kubedog/pkg/dyntracker/statestore"
	"github.com/werf/kubedog/pkg/dyntracker/util"
	"github.com/werf/kubedog/pkg/informer"
)

var replicaFailureDeploymentGVK = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}

func TestDynamicReadinessTracker_SingleDurableReplicaFailure_TerminatesPromptly(t *testing.T) {
	const boundedTimeout = 8 * time.Second

	tests := []struct {
		name          string
		allowFailures int
	}{
		{name: "allowance below the single failure", allowFailures: 0},
		{name: "allowance equal to the single failure", allowFailures: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dynClient := dynamicfake.NewSimpleDynamicClient(k8sscheme.Scheme,
				replicaFailureOneReplicaDeployment(), replicaFailureFailedCreateReplicaSet())
			kubeClient := k8sfake.NewSimpleClientset()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			watchErrCh := make(chan error, 100)
			factory := informer.NewConcurrentInformerFactory(ctx.Done(), watchErrCh, dynClient, informer.ConcurrentInformerFactoryOptions{})

			taskState := util.NewConcurrent(statestore.NewReadinessTaskState(
				"demo", "default", replicaFailureDeploymentGVK,
				statestore.ReadinessTaskStateOptions{TotalAllowFailuresCount: tt.allowFailures},
			))
			logStore := util.NewConcurrent(logstore.NewLogStore())

			rt, err := dyntracker.NewDynamicReadinessTracker(
				ctx, taskState, logStore, factory, kubeClient, dynClient, nil, replicaFailureStubRESTMapper{},
				dyntracker.DynamicReadinessTrackerOptions{
					Timeout:                    boundedTimeout,
					CaseInsensitiveGVKMatching: true,
					IgnoreLogs:                 true,
				},
			)
			if err != nil {
				t.Fatalf("NewDynamicReadinessTracker: %v", err)
			}

			start := time.Now()
			trackErr := rt.Track(ctx)
			elapsed := time.Since(start)

			if trackErr == nil {
				t.Fatalf("expected Track() to fail once the durable ReplicaFailure is observed, got nil error after %s", elapsed)
			}
			if !strings.Contains(trackErr.Error(), "readiness failed") {
				t.Fatalf("expected a readiness-failed error, got: %v (after %s)", trackErr, elapsed)
			}
			if elapsed >= boundedTimeout/2 {
				t.Fatalf("expected prompt termination well under the %s bailout bound, took %s: %v", boundedTimeout, elapsed, trackErr)
			}

			const expectedDiagnostic = `pods "demo-111" is forbidden: exceeded quota: compute-quota`
			var diagnosticFound bool
			taskState.RTransaction(func(state *statestore.ReadinessTaskState) {
				resourceState := state.ResourceState(state.Name(), state.Namespace(), state.GroupVersionKind())
				resourceState.RTransaction(func(resource *statestore.ResourceState) {
					for _, errs := range resource.Errors() {
						for _, resourceErr := range errs {
							if strings.Contains(resourceErr.Err.Error(), expectedDiagnostic) {
								diagnosticFound = true
							}
						}
					}
				})
			})
			if !diagnosticFound {
				t.Fatalf("task state does not retain ReplicaSet diagnostic %q", expectedDiagnostic)
			}
		})
	}
}

func replicaFailureOneReplicaDeployment() *appsv1.Deployment {
	one := int32(1)
	return &appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{Kind: "Deployment", APIVersion: "apps/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default", UID: "dep-uid-1", Generation: 1},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "demo"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "demo"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "busybox:1"}}},
			},
		},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 1},
	}
}

func replicaFailureFailedCreateReplicaSet() *appsv1.ReplicaSet {
	one := int32(1)
	isController := true
	return &appsv1.ReplicaSet{
		TypeMeta: metav1.TypeMeta{Kind: "ReplicaSet", APIVersion: "apps/v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo-111",
			Namespace: "default",
			UID:       "rs-uid-1",
			Labels:    map[string]string{"app": "demo"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "Deployment", Name: "demo", UID: "dep-uid-1", Controller: &isController,
			}},
			CreationTimestamp: metav1.Now(),
		},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: &one,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "demo"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "demo"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "busybox:1"}}},
			},
		},
		Status: appsv1.ReplicaSetStatus{
			Conditions: []appsv1.ReplicaSetCondition{{
				Type:               appsv1.ReplicaSetReplicaFailure,
				Status:             corev1.ConditionTrue,
				Reason:             "FailedCreate",
				Message:            `pods "demo-111" is forbidden: exceeded quota: compute-quota`,
				LastTransitionTime: metav1.Now(),
			}},
		},
	}
}

type replicaFailureStubRESTMapper struct{}

func (replicaFailureStubRESTMapper) KindFor(schema.GroupVersionResource) (schema.GroupVersionKind, error) {
	return schema.GroupVersionKind{}, errors.New("not implemented")
}

func (replicaFailureStubRESTMapper) KindsFor(schema.GroupVersionResource) ([]schema.GroupVersionKind, error) {
	return nil, errors.New("not implemented")
}

func (replicaFailureStubRESTMapper) ResourceFor(schema.GroupVersionResource) (schema.GroupVersionResource, error) {
	return schema.GroupVersionResource{}, errors.New("not implemented")
}

func (replicaFailureStubRESTMapper) ResourcesFor(schema.GroupVersionResource) ([]schema.GroupVersionResource, error) {
	return nil, errors.New("not implemented")
}

func (replicaFailureStubRESTMapper) RESTMapping(gk schema.GroupKind, _ ...string) (*meta.RESTMapping, error) {
	if strings.EqualFold(gk.Group, "apps") && strings.EqualFold(gk.Kind, "deployment") {
		return &meta.RESTMapping{
			Resource:         schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"},
			GroupVersionKind: replicaFailureDeploymentGVK,
			Scope:            meta.RESTScopeNamespace,
		}, nil
	}
	return nil, fmt.Errorf("replicaFailureStubRESTMapper: no mapping for %s", gk)
}

func (m replicaFailureStubRESTMapper) RESTMappings(gk schema.GroupKind, versions ...string) ([]*meta.RESTMapping, error) {
	mapping, err := m.RESTMapping(gk, versions...)
	if err != nil {
		return nil, err
	}
	return []*meta.RESTMapping{mapping}, nil
}

func (replicaFailureStubRESTMapper) ResourceSingularizer(resource string) (string, error) {
	return strings.TrimSuffix(resource, "s"), nil
}

func (replicaFailureStubRESTMapper) Reset() {}
