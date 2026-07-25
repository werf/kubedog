package deployment_test

import (
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/werf/kubedog/pkg/tracker/deployment"
)

func TestTrack_ForeignReplicaSetCannotMaskOwnedReplicaFailure(t *testing.T) {
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newNotReadyDeployment()},
	})

	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}

	createdAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	foreign := withReplicaSetCreationTimestamp(
		withReplicaSetControllerUID(newReplicaSet("foreign-111", "foreign-rs-uid"), "foreign-deployment-uid"),
		createdAt,
	)
	h.createObject(t, gvrReplicaSets, foreign)
	if _, err := h.waitFor("AddedReplicaSet", 10*time.Second, addedReplicaSetNamed(foreign.Name)); err != nil {
		t.Fatalf("wait for foreign AddedReplicaSet: %v", err)
	}

	const marker = "marker-owned-not-masked"
	owned := withReplicaFailure(
		withReplicaSetCreationTimestamp(newReplicaSet("demo-111", "owned-rs-uid"), createdAt.Add(time.Minute)),
		"FailedCreate",
		marker,
	)
	h.createObject(t, gvrReplicaSets, owned)

	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(marker)); err != nil {
		t.Fatalf("wait for owned ReplicaSet failure: %v", err)
	}
}

func TestTrack_IgnoresForeignAndOrphanReplicaSetFailures(t *testing.T) {
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newNotReadyDeployment()},
	})

	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}

	createdAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	const foreignMarker = "marker-foreign-failure"
	foreign := withReplicaFailure(
		withReplicaSetCreationTimestamp(
			withReplicaSetControllerUID(newReplicaSet("foreign-111", "foreign-rs-uid"), "foreign-deployment-uid"),
			createdAt,
		),
		"FailedCreate",
		foreignMarker,
	)
	h.createObject(t, gvrReplicaSets, foreign)
	if _, err := h.waitFor("AddedReplicaSet", 10*time.Second, addedReplicaSetNamed(foreign.Name)); err != nil {
		t.Fatalf("wait for foreign AddedReplicaSet: %v", err)
	}

	owned := withReplicaSetCreationTimestamp(newReplicaSet("demo-111", "owned-rs-uid"), createdAt.Add(2*time.Minute))
	h.createObject(t, gvrReplicaSets, owned)
	if _, err := h.waitFor("AddedReplicaSet", 10*time.Second, addedReplicaSetNamed(owned.Name)); err != nil {
		t.Fatalf("wait for owned AddedReplicaSet: %v", err)
	}

	if err := h.waitForNone("Failed", 2500*time.Millisecond, failedWithMarker(foreignMarker)); err != nil {
		t.Fatalf("foreign ReplicaSet must not fail the rollout: %v", err)
	}

	h.deleteObject(t, gvrReplicaSets, foreign.Name)

	const orphanMarker = "marker-orphan-failure"
	orphan := withReplicaFailure(
		withReplicaSetCreationTimestamp(withoutReplicaSetOwner(newReplicaSet("orphan-111", "orphan-rs-uid")), createdAt.Add(time.Minute)),
		"FailedCreate",
		orphanMarker,
	)
	h.createObject(t, gvrReplicaSets, orphan)
	if _, err := h.waitFor("AddedReplicaSet", 10*time.Second, addedReplicaSetNamed(orphan.Name)); err != nil {
		t.Fatalf("wait for orphan AddedReplicaSet: %v", err)
	}

	if err := h.waitForNone("Failed", 2500*time.Millisecond, failedWithMarker(orphanMarker)); err != nil {
		t.Fatalf("orphan ReplicaSet must not fail the rollout: %v", err)
	}
}

func TestTrack_DoesNotReportMessageOnlyReplicaFailureChanges(t *testing.T) {
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newNotReadyDeployment()},
	})

	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}

	rs := newReplicaSet("demo-111", "rs-uid-1")
	h.createObject(t, gvrReplicaSets, rs)
	if _, err := h.waitFor("AddedReplicaSet", 10*time.Second, addedNewReplicaSet(rs.Name)); err != nil {
		t.Fatalf("wait for AddedReplicaSet: %v", err)
	}

	const firstMarker = "marker-message-first"
	failed := withReplicaFailure(rs.DeepCopy(), "FailedCreate", firstMarker)
	h.updateReplicaSet(t, failed)
	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(firstMarker)); err != nil {
		t.Fatalf("wait for first Failed: %v", err)
	}

	const changedMarker = "marker-message-changed"
	messageChanged := failed.DeepCopy()
	messageChanged.Status.Conditions[0].Message = "updated quota details [" + changedMarker + "]"
	h.updateReplicaSet(t, messageChanged)

	if err := h.waitForNone("Failed", 2500*time.Millisecond, failedWithMarker(changedMarker)); err != nil {
		t.Fatalf("message-only failure change must not be reported again: %v", err)
	}
}

func TestTrack_IgnoresStaleEventsForRecreatedReplicaSet(t *testing.T) {
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newNotReadyDeployment()},
	})

	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}

	rsOld := newReplicaSet("demo-111", "rs-uid-old")
	h.createObject(t, gvrReplicaSets, rsOld)
	if _, err := h.waitFor("AddedReplicaSet", 10*time.Second, addedNewReplicaSet(rsOld.Name)); err != nil {
		t.Fatalf("wait for old AddedReplicaSet: %v", err)
	}
	h.deleteObject(t, gvrReplicaSets, rsOld.Name)

	const currentMarker = "marker-current-incarnation"
	rsCurrent := newReplicaSet("demo-111", "rs-uid-current")
	h.createObject(t, gvrReplicaSets, withReplicaFailure(rsCurrent.DeepCopy(), "FailedCreate", currentMarker))
	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(currentMarker)); err != nil {
		t.Fatalf("wait for current ReplicaSet failure: %v", err)
	}

	const staleMarker = "marker-stale-incarnation"
	h.tracker.TestOnlyInjectReplicaSetModified(withReplicaFailure(rsOld.DeepCopy(), "FailedCreate", staleMarker))
	h.tracker.TestOnlyInjectReplicaSetDeleted(rsOld.DeepCopy())
	if err := h.waitForNone("Failed", 2500*time.Millisecond, failedWithMarker(staleMarker)); err != nil {
		t.Fatalf("stale ReplicaSet events must not replace the current incarnation: %v", err)
	}

	h.updateReplicaSet(t, rsCurrent.DeepCopy())
	const recurringMarker = "marker-current-recurring"
	h.updateReplicaSet(t, withReplicaFailure(rsCurrent.DeepCopy(), "FailedCreate", recurringMarker))
	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(recurringMarker)); err != nil {
		t.Fatalf("current ReplicaSet must remain tracked after stale events: %v", err)
	}
}

func addedReplicaSetNamed(name string) func(observation) bool {
	return func(ev observation) bool {
		report, ok := ev.Data.(deployment.ReplicaSetAddedReport)
		return ok && report.ReplicaSet.Name == name
	}
}

func withReplicaSetControllerUID(rs *appsv1.ReplicaSet, uid types.UID) *appsv1.ReplicaSet {
	rs.OwnerReferences[0].UID = uid
	return rs
}

func withoutReplicaSetOwner(rs *appsv1.ReplicaSet) *appsv1.ReplicaSet {
	rs.OwnerReferences = nil
	return rs
}

func withReplicaSetCreationTimestamp(rs *appsv1.ReplicaSet, createdAt time.Time) *appsv1.ReplicaSet {
	rs.CreationTimestamp = metav1.NewTime(createdAt)
	return rs
}
