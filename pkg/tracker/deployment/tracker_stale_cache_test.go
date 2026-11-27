package deployment_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// The shared informer was started before the Deployment was applied and still
// caches the previous, fully rolled out generation, while the API server already
// has the applied one.
func TestTrack_IgnoresCachedGenerationOlderThanLive(t *testing.T) {
	live := newAppliedDeployment()

	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newReadyDeployment()},
		kubeSeed:    []runtime.Object{live},
	})

	if err := h.waitForNone("Ready", 3*time.Second, nil); err != nil {
		t.Fatalf("cached previous generation must not be reported ready: %v", err)
	}

	h.updateDeployment(t, live.DeepCopy())
	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}

	h.updateDeployment(t, rolledOut(live))
	if _, err := h.waitFor("Ready", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Ready: %v", err)
	}

	assertLiveStateList(t, h.kube)
}

// Events reach the tracker in the order the informer delivers them, even when
// several are pending while the tracker is busy re-reading the live state.
func TestTrack_IgnoresCachedGenerationQueuedBeforeLive(t *testing.T) {
	deleted := newReadyDeployment()
	deleted.UID = types.UID("dep-uid-0")
	live := newAppliedDeployment()

	rereading := make(chan struct{})
	release := make(chan struct{})
	var lists atomic.Int32
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{deleted},
		kubeSeed:    []runtime.Object{live},
		prepareKubeClient: func(c *k8sfake.Clientset) {
			c.PrependReactor("list", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
				if lists.Add(1) == 2 {
					close(rereading)
					<-release
				}

				return false, nil, nil
			})
		},
	})

	select {
	case <-rereading:
	case <-time.After(10 * time.Second):
		t.Fatal("live state was not re-read for the deleted incarnation")
	}

	h.deleteObject(t, gvrDeployments, testDeploymentName)
	h.createObject(t, gvrDeployments, newReadyDeployment())
	h.updateDeployment(t, live.DeepCopy())
	// Let the informer queue the events while the tracker is still re-reading.
	time.Sleep(500 * time.Millisecond)
	close(release)

	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}
	if err := h.waitForNone("Ready", 2*time.Second, nil); err != nil {
		t.Fatalf("cached previous generation must not be reported ready: %v", err)
	}
}

func TestTrack_ReportsCachedLiveGenerationReady(t *testing.T) {
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newReadyDeployment()},
		kubeSeed:    []runtime.Object{newReadyDeployment()},
	})

	if _, err := h.waitFor("Ready", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Ready: %v", err)
	}
}

// The Deployment was recreated, but the shared informer still caches the
// deleted incarnation.
func TestTrack_IgnoresCachedDeletedIncarnation(t *testing.T) {
	cached := newReadyDeployment()
	cached.Generation = 5
	cached.Status.ObservedGeneration = 5

	live := newNotReadyDeployment()
	live.UID = types.UID("dep-uid-2")

	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{cached},
		kubeSeed:    []runtime.Object{live},
	})

	if err := h.waitForNone("Ready", 3*time.Second, nil); err != nil {
		t.Fatalf("cached deleted incarnation must not be reported ready: %v", err)
	}

	h.deleteObject(t, gvrDeployments, testDeploymentName)
	h.createObject(t, gvrDeployments, live.DeepCopy())
	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}
}

// The Deployment does not exist when tracking starts, but the shared informer
// still caches a deleted incarnation.
func TestTrack_IgnoresCachedIncarnationDeletedBeforeTrackingStarted(t *testing.T) {
	h := newHarness(t, harnessConfig{
		dynamicSeed:      []runtime.Object{newReadyDeployment()},
		noLiveDeployment: true,
	})

	if err := h.waitForNone("Ready", 3*time.Second, nil); err != nil {
		t.Fatalf("cached deleted incarnation must not be reported ready: %v", err)
	}

	created := newNotReadyDeployment()
	created.UID = types.UID("dep-uid-2")
	h.recreateLive(t, created)
	h.deleteObject(t, gvrDeployments, testDeploymentName)
	h.createObject(t, gvrDeployments, created)
	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}
}

// The live Deployment is deleted and recreated before the shared informer
// delivered its applied generation: tracking must follow the new incarnation.
func TestTrack_FollowsRecreationAfterDeletingLiveIncarnation(t *testing.T) {
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newReadyDeployment()},
		kubeSeed:    []runtime.Object{newAppliedDeployment()},
	})

	if err := h.waitForNone("Ready", 3*time.Second, nil); err != nil {
		t.Fatalf("cached previous generation must not be reported ready: %v", err)
	}

	recreated := newNotReadyDeployment()
	recreated.UID = types.UID("dep-uid-2")
	h.recreateLive(t, recreated)
	h.deleteObject(t, gvrDeployments, testDeploymentName)
	h.createObject(t, gvrDeployments, recreated)
	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}

	rs := newReplicaSet("demo-222", "rs-uid-2")
	rs.OwnerReferences[0].UID = recreated.UID
	h.createObject(t, gvrReplicaSets, rs)
	if _, err := h.waitFor("AddedReplicaSet", 10*time.Second, addedNewReplicaSet(rs.Name)); err != nil {
		t.Fatalf("recreated Deployment must be tracked with its ReplicaSets: %v", err)
	}
}

// The Deployment was recreated after the live state was read, and the informer
// never delivers the deletion of the incarnation that was read.
func TestTrack_FollowsIncarnationCreatedAfterLiveStateWasRead(t *testing.T) {
	recreated := newNotReadyDeployment()
	recreated.UID = types.UID("dep-uid-2")

	var lists atomic.Int32
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{recreated},
		prepareKubeClient: func(c *k8sfake.Clientset) {
			c.PrependReactor("list", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
				dep := recreated
				if lists.Add(1) == 1 {
					dep = newAppliedDeployment()
				}

				return true, &appsv1.DeploymentList{Items: []appsv1.Deployment{*dep}}, nil
			})
		},
	})

	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}
}

func TestTrack_FailsWhenLiveStateUnavailable(t *testing.T) {
	errList := errors.New("list failed")
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newReadyDeployment()},
		prepareKubeClient: func(c *k8sfake.Clientset) {
			c.PrependReactor("list", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, errList
			})
		},
	})

	select {
	case <-h.done:
	case <-time.After(10 * time.Second):
		t.Fatal("Track() did not exit")
	}
	if err := h.getTrackErr(); !errors.Is(err, errList) {
		t.Fatalf("expected the list error, got: %v", err)
	}
}

// newAppliedDeployment is the next generation of newReadyDeployment, not yet
// observed by the Deployment controller.
func newAppliedDeployment() *appsv1.Deployment {
	dep := newNotReadyDeployment()
	dep.Generation = 2
	dep.Spec.Template.Spec.Containers[0].Image = "busybox:2"
	return dep
}

func rolledOut(dep *appsv1.Deployment) *appsv1.Deployment {
	dep = dep.DeepCopy()
	dep.Status = appsv1.DeploymentStatus{
		ObservedGeneration: dep.Generation,
		Replicas:           1,
		UpdatedReplicas:    1,
		AvailableReplicas:  1,
	}
	return dep
}

// recreateLive replaces the Deployment the tracker reads its live state from.
func (h *harness) recreateLive(t *testing.T, dep *appsv1.Deployment) {
	t.Helper()
	ctx := context.Background()
	deployments := h.kube.AppsV1().Deployments(testNamespace)
	if err := deployments.Delete(ctx, dep.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		t.Fatalf("delete live deployment: %v", err)
	}
	if _, err := deployments.Create(ctx, dep.DeepCopy(), metav1.CreateOptions{}); err != nil {
		t.Fatalf("create live deployment: %v", err)
	}
}

// The live state must be a read of this Deployment only. This client-go's fake does not
// record the resource version, so a consistent read is not checked here.
func assertLiveStateList(t *testing.T, kube *k8sfake.Clientset) {
	t.Helper()
	for _, action := range kube.Actions() {
		list, ok := action.(k8stesting.ListActionImpl)
		if !ok || list.GetResource().Resource != "deployments" {
			continue
		}

		if list.GetNamespace() != testNamespace ||
			list.ListRestrictions.Fields.String() != "metadata.name="+testDeploymentName {
			t.Fatalf("unexpected live state list: namespace %q, restrictions %+v", list.GetNamespace(), list.ListRestrictions)
		}

		return
	}
	t.Fatal("live state was not listed")
}
