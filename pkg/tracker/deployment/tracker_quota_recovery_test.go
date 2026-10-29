package deployment_test

import (
	"errors"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
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

func TestTrack_ClassifiesQuotaFailureByWhetherTheRolloutCanReleaseQuota(t *testing.T) {
	type rollout struct {
		replicas, maxUnavailable, maxSurge  int32
		oldSpec, newSpec, newStatusReplicas int32
		newPods                             []*corev1.Pod
		quota                               *corev1.ResourceQuota
		forbidQuota                         bool
	}

	tests := []struct {
		name    string
		rollout rollout
		want    deployment.FailureMode
	}{
		{
			// The pod created in the failed sync is missing from the status: the
			// ReplicaSet controller counts the pods it listed before creating any.
			name: "pod created before the failure can become available and scale an old pod down",
			rollout: rollout{
				replicas: 3, maxUnavailable: 0, maxSurge: 2,
				oldSpec: 3, newSpec: 2, newStatusReplicas: 0,
				newPods: []*corev1.Pod{newReplicaSetPod("demo-new-1", "rs-uid-new")},
			},
			want: deployment.FailureModeCounted,
		},
		{
			name: "status already counts the pod created before the failure",
			rollout: rollout{
				replicas: 3, maxUnavailable: 0, maxSurge: 2,
				oldSpec: 3, newSpec: 2, newStatusReplicas: 1,
			},
			want: deployment.FailureModeCounted,
		},
		{
			name: "terminating pod releases quota",
			rollout: rollout{
				replicas: 3, maxUnavailable: 0, maxSurge: 1,
				oldSpec: 2, newSpec: 2, newStatusReplicas: 1,
				newPods: []*corev1.Pod{
					newReplicaSetPod("demo-new-1", "rs-uid-new"),
					terminating(newReplicaSetPod("demo-old-1", "rs-uid-old")),
				},
			},
			want: deployment.FailureModeCounted,
		},
		{
			// The ReplicaSet controller has not retried since an old pod went away.
			name: "quota has room again",
			rollout: rollout{
				replicas: 3, maxUnavailable: 0, maxSurge: 1,
				oldSpec: 2, newSpec: 2, newStatusReplicas: 1,
				newPods: []*corev1.Pod{newReplicaSetPod("demo-new-1", "rs-uid-new")},
				quota:   computeQuota(4, 3),
			},
			want: deployment.FailureModeCounted,
		},
		{
			name: "quota no longer exists",
			rollout: rollout{
				replicas: 3, maxUnavailable: 0, maxSurge: 1,
				oldSpec: 2, newSpec: 2, newStatusReplicas: 1,
				newPods: []*corev1.Pod{newReplicaSetPod("demo-new-1", "rs-uid-new")},
			},
			want: deployment.FailureModeCounted,
		},
		{
			name: "quota cannot be read",
			rollout: rollout{
				replicas: 3, maxUnavailable: 0, maxSurge: 1,
				oldSpec: 2, newSpec: 2, newStatusReplicas: 1,
				newPods:     []*corev1.Pod{newReplicaSetPod("demo-new-1", "rs-uid-new")},
				quota:       computeQuota(3, 3),
				forbidQuota: true,
			},
			want: deployment.FailureModeCounted,
		},
		{
			name: "new pod and old pods hold all of the quota",
			rollout: rollout{
				replicas: 3, maxUnavailable: 0, maxSurge: 1,
				oldSpec: 2, newSpec: 2, newStatusReplicas: 1,
				newPods: []*corev1.Pod{newReplicaSetPod("demo-new-1", "rs-uid-new")},
				quota:   computeQuota(3, 3),
			},
			want: deployment.FailureModeFatal,
		},
		{
			// A cached ReplicaSet may still carry the condition after a retry created every pod.
			name: "every desired new pod exists",
			rollout: rollout{
				replicas: 2, maxUnavailable: 0, maxSurge: 1,
				oldSpec: 0, newSpec: 2, newStatusReplicas: 1,
				newPods: []*corev1.Pod{
					newReplicaSetPod("demo-new-1", "rs-uid-new"),
					newReplicaSetPod("demo-new-2", "rs-uid-new"),
				},
				quota: computeQuota(2, 2),
			},
			want: deployment.FailureModeCounted,
		},
		{
			name: "no new pod can be created and no old pod may go away",
			rollout: rollout{
				replicas: 3, maxUnavailable: 0, maxSurge: 2,
				oldSpec: 3, newSpec: 2, newStatusReplicas: 0,
				quota: computeQuota(3, 3),
			},
			want: deployment.FailureModeFatal,
		},
		{
			name: "single replica with the default strategy cannot surge",
			rollout: rollout{
				replicas: 1, maxUnavailable: 0, maxSurge: 1,
				oldSpec: 1, newSpec: 1, newStatusReplicas: 0,
				quota: computeQuota(1, 1),
			},
			want: deployment.FailureModeFatal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := tt.rollout

			deploymentObject := rollingDeployment(r.maxUnavailable, r.maxSurge)
			deploymentObject.Spec.Replicas = ptrTo(r.replicas)

			oldReplicaSet := scalableOldReplicaSet()
			oldReplicaSet.Spec.Replicas = ptrTo(r.oldSpec)
			oldReplicaSet.Status.Replicas = r.oldSpec
			oldReplicaSet.Status.AvailableReplicas = r.oldSpec

			const marker = "marker-quota-classification"
			newReplicaSet := withReplicaFailure(newReplicaSet("demo-new", "rs-uid-new"), "FailedCreate", marker)
			newReplicaSet.Spec.Replicas = ptrTo(r.newSpec)
			newReplicaSet.Status.Replicas = r.newStatusReplicas

			kubeSeed := []runtime.Object{}
			if r.quota != nil {
				kubeSeed = append(kubeSeed, r.quota)
			}
			for _, pod := range r.newPods {
				kubeSeed = append(kubeSeed, pod)
			}

			var prepareKubeClient func(*k8sfake.Clientset)
			if r.forbidQuota {
				prepareKubeClient = forbidResourceQuotas
			}

			h := newHarness(t, harnessConfig{
				dynamicSeed:          []runtime.Object{deploymentObject},
				prepareDynamicClient: initialReplicaSetListReactor(t, newReplicaSet, oldReplicaSet),
				kubeSeed:             kubeSeed,
				prepareKubeClient:    prepareKubeClient,
			})
			if _, err := h.waitFor("Failed", 10*time.Second, failedWithMode(marker, tt.want)); err != nil {
				t.Fatalf("want quota failure classified as %v: %v", tt.want, err)
			}
		})
	}
}

func TestTrack_ConfirmsQuotaDeadlockBeforeReportingItFatal(t *testing.T) {
	const confirmation = 2 * time.Second

	tests := []struct {
		name                 string
		releaseQuotaMeantime bool
		want                 deployment.FailureMode
	}{
		{name: "deadlock persists", want: deployment.FailureModeFatal},
		{
			// The quota controller lowers the usage only after it notices a deleted pod.
			name:                 "quota usage drops before the confirmation",
			releaseQuotaMeantime: true,
			want:                 deployment.FailureModeCounted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deploymentObject := rollingDeployment(0, 1)
			deploymentObject.Spec.Replicas = ptrTo(int32(3))

			oldReplicaSet := scalableOldReplicaSet()
			oldReplicaSet.Spec.Replicas = ptrTo(int32(2))
			oldReplicaSet.Status.Replicas = 2
			oldReplicaSet.Status.AvailableReplicas = 2

			const marker = "marker-quota-confirmation"
			newReplicaSet := withReplicaFailure(newReplicaSet("demo-new", "rs-uid-new"), "FailedCreate", marker)
			newReplicaSet.Spec.Replicas = ptrTo(int32(2))
			newReplicaSet.Status.Replicas = 1

			h := newHarness(t, harnessConfig{
				dynamicSeed:               []runtime.Object{deploymentObject},
				prepareDynamicClient:      initialReplicaSetListReactor(t, newReplicaSet, oldReplicaSet),
				kubeSeed:                  []runtime.Object{computeQuota(3, 3), newReplicaSetPod("demo-new-1", "rs-uid-new")},
				quotaDeadlockConfirmation: confirmation,
			})

			waitForQuotaRead(t, h)
			if tt.releaseQuotaMeantime {
				if _, err := h.kube.CoreV1().ResourceQuotas(testNamespace).UpdateStatus(h.ctx, computeQuota(3, 2), metav1.UpdateOptions{}); err != nil {
					t.Fatalf("update quota status: %v", err)
				}
			}

			if err := h.waitForNone("Failed", confirmation/2, nil); err != nil {
				t.Fatalf("quota failure must stay undecided until the deadlock is confirmed: %v", err)
			}
			if _, err := h.waitFor("Failed", 10*time.Second, failedWithMode(marker, tt.want)); err != nil {
				t.Fatalf("want quota failure classified as %v after the confirmation: %v", tt.want, err)
			}
		})
	}
}

func waitForQuotaRead(t *testing.T, h *harness) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, action := range h.kube.Actions() {
			if action.Matches("get", "resourcequotas") {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("tracker did not read the resource quota")
}

func forbidResourceQuotas(client *k8sfake.Clientset) {
	client.PrependReactor("get", "resourcequotas", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(corev1.Resource("resourcequotas"), "compute-quota", errors.New("denied"))
	})
}

func newReplicaSetPod(name string, replicaSetUID types.UID) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			Labels:    map[string]string{"app": "demo"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "ReplicaSet", Name: name, UID: replicaSetUID, Controller: ptrTo(true),
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodPending},
	}
}

func terminating(pod *corev1.Pod) *corev1.Pod {
	pod.DeletionTimestamp = ptrTo(metav1.Now())
	pod.Finalizers = []string{"example.com/keep"}
	return pod
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
