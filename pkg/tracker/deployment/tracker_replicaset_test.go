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

func TestTrack_IgnoresReplicaFailureWhenDeploymentStatusIsReady(t *testing.T) {
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newReadyDeployment()},
	})

	if _, err := h.waitFor("Ready", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Ready: %v", err)
	}

	const marker = "marker-ready-deployment-wins"
	h.createObject(t, gvrReplicaSets, withReplicaFailure(newReplicaSet("demo-111", "rs-uid-1"), "FailedCreate", marker))

	if _, err := h.waitFor("AddedReplicaSet", 10*time.Second, addedReplicaSetNamed("demo-111")); err != nil {
		t.Fatalf("wait for AddedReplicaSet: %v", err)
	}

	if err := h.waitForNone("Failed", 2500*time.Millisecond, failedWithMarker(marker)); err != nil {
		t.Fatal(err)
	}
}

func TestTrack_FailsOnReplicaFailureWhileDeploymentIsNotReady(t *testing.T) {
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newNotReadyDeployment()},
	})

	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}

	const marker = "marker-not-ready-deployment-fails"
	h.createObject(t, gvrReplicaSets, withReplicaFailure(newReplicaSet("demo-111", "rs-uid-1"), "FailedCreate", marker))

	ev, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(marker))
	if err != nil {
		t.Fatalf("wait for Failed: %v", err)
	}

	status, ok := ev.Data.(deployment.DeploymentStatus)
	if !ok {
		t.Fatalf("unexpected Failed payload: %T", ev.Data)
	}
	if status.IsReady {
		t.Fatalf("reported failure must not be ready: %+v", status)
	}

	if err := h.waitForNone("Ready", 2500*time.Millisecond, nil); err != nil {
		t.Fatal(err)
	}
}

func TestTrack_ReportsReplicaFailureAgainAfterDeploymentStoppedBeingReady(t *testing.T) {
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newNotReadyDeployment()},
	})

	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}

	const marker = "marker-readiness-rearms-reporting"
	h.createObject(t, gvrReplicaSets, withReplicaFailure(newReplicaSet("demo-111", "rs-uid-1"), "FailedCreate", marker))
	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(marker)); err != nil {
		t.Fatalf("wait for first Failed: %v", err)
	}

	h.updateDeployment(t, newReadyDeployment())
	if _, err := h.waitFor("Ready", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Ready: %v", err)
	}

	replicaLost := newReadyDeployment()
	replicaLost.Status.AvailableReplicas = 0
	replicaLost.Status.UnavailableReplicas = 1
	h.updateDeployment(t, replicaLost)

	ev, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(marker))
	if err != nil {
		t.Fatalf("a Deployment that stopped being ready must be failed anew by the condition it recovered from: %v", err)
	}

	status, ok := ev.Data.(deployment.DeploymentStatus)
	if !ok {
		t.Fatalf("unexpected Failed payload: %T", ev.Data)
	}
	if status.IsReady {
		t.Fatalf("reported failure must not be ready: %+v", status)
	}
	if status.AvailableReplicas != 0 || status.UnavailableReplicas != 1 {
		t.Fatalf("reported failure must describe the lost replica: %+v", status)
	}
}

func TestTrack_ReportsReplicaFailureAgainAfterRollbackToRecoveredReplicaSet(t *testing.T) {
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newNotReadyDeployment()},
	})

	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}

	const firstMarker = "marker-before-rollout"
	h.createObject(t, gvrReplicaSets, withReplicaFailure(newReplicaSet("demo-111", "rs-uid-1"), "FailedCreate", firstMarker))
	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(firstMarker)); err != nil {
		t.Fatalf("wait for first Failed: %v", err)
	}

	rolledOut := newNotReadyDeployment()
	rolledOut.Spec.Template.Spec.Containers[0].Image = "busybox:2"
	rolledOut.Status.Replicas = 1
	h.updateDeployment(t, rolledOut)
	if _, err := h.waitFor("Status", 10*time.Second, statusWithReplicas(1)); err != nil {
		t.Fatalf("wait for rollout Status: %v", err)
	}

	successor := newReplicaSet("demo-222", "rs-uid-2")
	successor.Spec.Template.Spec.Containers[0].Image = "busybox:2"
	h.createObject(t, gvrReplicaSets, successor)
	if _, err := h.waitFor("AddedReplicaSet", 10*time.Second, addedNewReplicaSet(successor.Name)); err != nil {
		t.Fatalf("wait for successor AddedReplicaSet: %v", err)
	}

	h.updateReplicaSet(t, newReplicaSet("demo-111", "rs-uid-1"))

	const secondMarker = "marker-after-rollback"
	h.updateReplicaSet(t, withReplicaFailure(newReplicaSet("demo-111", "rs-uid-1"), "FailedCreate", secondMarker))
	if err := h.waitForNone("Failed", 2500*time.Millisecond, failedWithMarker(secondMarker)); err != nil {
		t.Fatalf("previous rollout ReplicaSet must not fail the current rollout: %v", err)
	}

	h.updateDeployment(t, newNotReadyDeployment())
	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(secondMarker)); err != nil {
		t.Fatalf("failure of the ReplicaSet rolled back onto must be reported anew: %v", err)
	}
}

func TestTrack_ReportsReplicaFailureOnceAcrossStatusUpdates(t *testing.T) {
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newNotReadyDeployment()},
	})

	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}

	const marker = "marker-reported-once"
	h.createObject(t, gvrReplicaSets, withReplicaFailure(newReplicaSet("demo-111", "rs-uid-1"), "FailedCreate", marker))
	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(marker)); err != nil {
		t.Fatalf("wait for Failed: %v", err)
	}

	dep := newNotReadyDeployment()
	dep.Status.Replicas = 1
	h.updateDeployment(t, dep)

	ev, err := h.waitFor("Status", 10*time.Second, statusWithReplicas(1))
	if err != nil {
		t.Fatalf("wait for Status: %v", err)
	}
	status, ok := ev.Data.(deployment.DeploymentStatus)
	if !ok {
		t.Fatalf("unexpected Status payload: %T", ev.Data)
	}
	if status.IsFailed || status.FailedReason != "" {
		t.Fatalf("routine status must not carry the already reported failure: %+v", status)
	}

	if err := h.waitForNone("Failed", 2500*time.Millisecond, failedWithMarker(marker)); err != nil {
		t.Fatal(err)
	}
}

func TestTrack_StopsReportingWithdrawnReplicaFailure(t *testing.T) {
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newNotReadyDeployment()},
	})

	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}

	rs := newReplicaSet("demo-111", "rs-uid-1")
	h.createObject(t, gvrReplicaSets, rs)
	if _, err := h.waitFor("AddedReplicaSet", 10*time.Second, addedReplicaSetNamed(rs.Name)); err != nil {
		t.Fatalf("wait for AddedReplicaSet: %v", err)
	}

	const withdrawnMarker = "marker-withdrawn"
	h.updateReplicaSet(t, withReplicaFailure(rs.DeepCopy(), "FailedCreate", withdrawnMarker))
	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(withdrawnMarker)); err != nil {
		t.Fatalf("wait for Failed: %v", err)
	}

	h.updateReplicaSet(t, rs.DeepCopy())

	dep := newNotReadyDeployment()
	dep.Status.Replicas = 1
	h.updateDeployment(t, dep)
	if err := h.waitForNone("Failed", 2500*time.Millisecond, failedWithMarker(withdrawnMarker)); err != nil {
		t.Fatal(err)
	}

	const rearmedMarker = "marker-rearmed"
	h.updateReplicaSet(t, withReplicaFailure(rs.DeepCopy(), "FailedCreate", rearmedMarker))
	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(rearmedMarker)); err != nil {
		t.Fatalf("failure must be reported again after the condition reappeared: %v", err)
	}
}

func TestTrack_IgnoresStaleModifiedDeliveredAfterReplicaSetDeletion(t *testing.T) {
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newNotReadyDeployment()},
	})

	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}

	createdAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	rsOld := withReplicaSetCreationTimestamp(newReplicaSet("demo-111", "rs-uid-old"), createdAt)
	h.createObject(t, gvrReplicaSets, rsOld)
	if _, err := h.waitFor("AddedReplicaSet", 10*time.Second, addedReplicaSetNamed(rsOld.Name)); err != nil {
		t.Fatalf("wait for AddedReplicaSet: %v", err)
	}

	h.injectReplicaSetDeletedAndWait(t, rsOld.DeepCopy())

	rsCurrent := withReplicaSetCreationTimestamp(newReplicaSet("demo-222", "rs-uid-current"), createdAt.Add(time.Minute))
	h.createObject(t, gvrReplicaSets, rsCurrent)
	if _, err := h.waitFor("AddedReplicaSet", 10*time.Second, addedReplicaSetNamed(rsCurrent.Name)); err != nil {
		t.Fatalf("wait for AddedReplicaSet: %v", err)
	}

	const staleMarker = "marker-stale"
	h.tracker.TestOnlyInjectReplicaSetModified(withReplicaFailure(rsOld.DeepCopy(), "FailedCreate", staleMarker))
	if err := h.waitForNone("Failed", 2500*time.Millisecond, failedWithMarker(staleMarker)); err != nil {
		t.Fatal(err)
	}

	const currentMarker = "marker-current"
	h.updateReplicaSet(t, withReplicaFailure(rsCurrent.DeepCopy(), "FailedCreate", currentMarker))
	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(currentMarker)); err != nil {
		t.Fatalf("current ReplicaSet must remain tracked after the stale event: %v", err)
	}
}

func TestTrack_ReportsFailureOfReplicaSetPromotedByDeletion(t *testing.T) {
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newNotReadyDeployment()},
	})

	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}

	createdAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	current := withReplicaSetCreationTimestamp(newReplicaSet("demo-111", "rs-uid-current"), createdAt)
	h.createObject(t, gvrReplicaSets, current)
	if _, err := h.waitFor("AddedReplicaSet", 10*time.Second, addedNewReplicaSet(current.Name)); err != nil {
		t.Fatalf("wait for current AddedReplicaSet: %v", err)
	}

	const marker = "marker-promoted-by-deletion"
	duplicate := withReplicaFailure(
		withReplicaSetCreationTimestamp(newReplicaSet("demo-222", "rs-uid-duplicate"), createdAt.Add(time.Minute)),
		"FailedCreate",
		marker,
	)
	h.createObject(t, gvrReplicaSets, duplicate)
	if _, err := h.waitFor("AddedReplicaSet", 10*time.Second, addedReplicaSetNamed(duplicate.Name)); err != nil {
		t.Fatalf("wait for duplicate AddedReplicaSet: %v", err)
	}

	if err := h.waitForNone("Failed", 2500*time.Millisecond, failedWithMarker(marker)); err != nil {
		t.Fatalf("a ReplicaSet that is not the new one must not fail the rollout: %v", err)
	}

	h.deleteObject(t, gvrReplicaSets, current.Name)

	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(marker)); err != nil {
		t.Fatalf("failure of the ReplicaSet promoted by the deletion must be reported: %v", err)
	}
}

func TestTrack_PreservesReplicaSetEventOrderAcrossChannels(t *testing.T) {
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newNotReadyDeployment()},
	})

	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}

	rs := newReplicaSet("demo-111", "rs-uid-1")
	const initialMarker = "marker-initial-failure"
	const recurringMarker = "marker-recurring-failure"

	// Occupies the tracker so the events below arrive while it is busy: only then can
	// buffered fan-out channels reorder them and make this test a discriminator.
	h.tracker.TestOnlyInjectResourceFailure("keeps the tracker busy")
	h.tracker.TestOnlyInjectReplicaSetAdded(withReplicaFailure(rs.DeepCopy(), "FailedCreate", initialMarker))
	h.tracker.TestOnlyInjectReplicaSetModified(rs.DeepCopy())
	h.tracker.TestOnlyInjectReplicaSetModified(withReplicaFailure(rs.DeepCopy(), "FailedCreate", recurringMarker))

	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(initialMarker)); err != nil {
		t.Fatalf("initial failure must be reported despite fan-out across channels: %v", err)
	}
	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(recurringMarker)); err != nil {
		t.Fatalf("failure recurring after recovery must be reported anew: %v", err)
	}
}

func addedReplicaSetNamed(name string) func(observation) bool {
	return func(ev observation) bool {
		report, ok := ev.Data.(deployment.ReplicaSetAddedReport)
		return ok && report.ReplicaSet.Name == name
	}
}

func statusWithReplicas(replicas int32) func(observation) bool {
	return func(ev observation) bool {
		status, ok := ev.Data.(deployment.DeploymentStatus)
		return ok && status.Replicas == replicas
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
