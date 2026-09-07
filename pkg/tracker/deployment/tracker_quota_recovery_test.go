package deployment_test

import (
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/werf/kubedog/pkg/tracker/deployment"
)

func TestTrack_RecoversFromQuotaFailureWhenOldReplicaSetCanScaleDown(t *testing.T) {
	deploymentObject := rollingDeployment(1, 1)
	h := newHarness(t, harnessConfig{dynamicSeed: []runtime.Object{deploymentObject}})
	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}

	oldReplicaSet := scalableOldReplicaSet()
	h.createObject(t, gvrReplicaSets, oldReplicaSet)
	if _, err := h.waitFor("AddedReplicaSet", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for old AddedReplicaSet: %v", err)
	}

	const marker = "marker-quota-recoverable-by-scale-down"
	newReplicaSet := withReplicaFailure(newReplicaSet("demo-new", "rs-uid-new"), "FailedCreate", marker)
	h.createObject(t, gvrReplicaSets, newReplicaSet)
	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMode(marker, deployment.FailureModeCounted)); err != nil {
		t.Fatalf("quota failure must remain recoverable while an old ReplicaSet can scale down: %v", err)
	}
}

func TestTrack_RecoversFromQuotaFailureWhileOldReplicaSetPodsAreTerminating(t *testing.T) {
	deploymentObject := rollingDeployment(0, 1)
	h := newHarness(t, harnessConfig{dynamicSeed: []runtime.Object{deploymentObject}})
	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}

	oldReplicaSet := scalableOldReplicaSet()
	*oldReplicaSet.Spec.Replicas = 0
	oldReplicaSet.Status.Replicas = 0
	oldReplicaSet.Status.AvailableReplicas = 0
	oldReplicaSet.Status.TerminatingReplicas = ptrTo(int32(1))
	h.createObject(t, gvrReplicaSets, oldReplicaSet)
	if _, err := h.waitFor("AddedReplicaSet", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for terminating old AddedReplicaSet: %v", err)
	}

	const marker = "marker-quota-recoverable-by-termination"
	newReplicaSet := withReplicaFailure(newReplicaSet("demo-new", "rs-uid-new"), "FailedCreate", marker)
	h.createObject(t, gvrReplicaSets, newReplicaSet)
	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMode(marker, deployment.FailureModeCounted)); err != nil {
		t.Fatalf("quota failure must remain recoverable while old Pods terminate: %v", err)
	}
}

func TestTrack_ClassifiesInitialQuotaFailureAfterAllReplicaSetsAreObserved(t *testing.T) {
	deploymentObject := rollingDeployment(1, 1)
	oldReplicaSet := scalableOldReplicaSet()
	const marker = "marker-quota-initial-snapshot"
	newReplicaSet := withReplicaFailure(newReplicaSet("demo-new", "rs-uid-new"), "FailedCreate", marker)

	h := newHarness(t, harnessConfig{
		dynamicSeed:          []runtime.Object{deploymentObject},
		prepareDynamicClient: initialReplicaSetListReactor(t, newReplicaSet, oldReplicaSet),
	})
	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMode(marker, deployment.FailureModeCounted)); err != nil {
		t.Fatalf("initial failure must use the complete ReplicaSet snapshot: %v", err)
	}
}

func TestTrack_DoesNotEscalateStaleQuotaFailureAfterOldReplicaSetScalesDown(t *testing.T) {
	deploymentObject := rollingDeployment(1, 1)
	h := newHarness(t, harnessConfig{dynamicSeed: []runtime.Object{deploymentObject}})
	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}

	oldReplicaSet := scalableOldReplicaSet()
	h.createObject(t, gvrReplicaSets, oldReplicaSet)
	if _, err := h.waitFor("AddedReplicaSet", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for old AddedReplicaSet: %v", err)
	}

	const marker = "marker-quota-stale-after-scale-down"
	newReplicaSet := withReplicaFailure(newReplicaSet("demo-new", "rs-uid-new"), "FailedCreate", marker)
	h.createObject(t, gvrReplicaSets, newReplicaSet)
	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMode(marker, deployment.FailureModeCounted)); err != nil {
		t.Fatalf("wait for initial counted failure: %v", err)
	}

	scaledDown := oldReplicaSet.DeepCopy()
	*scaledDown.Spec.Replicas = 0
	scaledDown.Status.Replicas = 0
	scaledDown.Status.AvailableReplicas = 0
	h.updateReplicaSet(t, scaledDown)
	if err := h.waitForNone("Failed", 2500*time.Millisecond, failedWithMode(marker, deployment.FailureModeFatal)); err != nil {
		t.Fatalf("stale quota condition must not become fatal while the ReplicaSet controller retries: %v", err)
	}
}

func rollingDeployment(maxUnavailable, maxSurge int32) *appsv1.Deployment {
	deploymentObject := newNotReadyDeployment()
	unavailable := intstr.FromInt32(maxUnavailable)
	surge := intstr.FromInt32(maxSurge)
	deploymentObject.Spec.Strategy = appsv1.DeploymentStrategy{
		Type: appsv1.RollingUpdateDeploymentStrategyType,
		RollingUpdate: &appsv1.RollingUpdateDeployment{
			MaxUnavailable: &unavailable,
			MaxSurge:       &surge,
		},
	}
	return deploymentObject
}

func scalableOldReplicaSet() *appsv1.ReplicaSet {
	oldReplicaSet := newReplicaSet("demo-old", "rs-uid-old")
	oldReplicaSet.Spec.Template.Spec.Containers[0].Image = "busybox:old"
	oldReplicaSet.Status.Replicas = 1
	oldReplicaSet.Status.AvailableReplicas = 1
	return oldReplicaSet
}

func initialReplicaSetListReactor(t *testing.T, replicaSets ...*appsv1.ReplicaSet) func(*dynamicfake.FakeDynamicClient) {
	t.Helper()

	return func(client *dynamicfake.FakeDynamicClient) {
		client.PrependReactor("list", "replicasets", func(k8stesting.Action) (bool, runtime.Object, error) {
			list := &unstructured.UnstructuredList{}
			list.SetGroupVersionKind(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "ReplicaSetList"})
			for _, replicaSet := range replicaSets {
				list.Items = append(list.Items, *mustToUnstructured(t, replicaSet))
			}
			return true, list, nil
		})
	}
}
