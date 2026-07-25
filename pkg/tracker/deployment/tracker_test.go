package deployment_test

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/werf/kubedog/pkg/tracker/deployment"
)

func TestTrack_FailsOnNewReplicaSetReplicaFailure(t *testing.T) {
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newNotReadyDeployment()},
	})

	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}

	const marker = "marker-happy-path"
	h.createObject(t, gvrReplicaSets, withReplicaFailure(newReplicaSet("demo-111", "rs-uid-1"), "FailedCreate", marker))

	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(marker)); err != nil {
		t.Fatalf("wait for Failed: %v", err)
	}
}

func TestTrack_FailsOnReplicaFailureAppearingAfterReplicaSetAdded(t *testing.T) {
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

	const marker = "marker-condition-after-add"
	h.updateReplicaSet(t, withReplicaFailure(rs.DeepCopy(), "FailedCreate", marker))

	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(marker)); err != nil {
		t.Fatalf("wait for Failed: %v", err)
	}
}

// Models the apply-then-track gap: the ReplicaSet already carries the failure
// condition when tracking starts, so unlike a Warning event there is no history
// to replay and no start boundary to reason about.
func TestTrack_FailsOnPreexistingReplicaFailure(t *testing.T) {
	const marker = "marker-preexisting"
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{
			newNotReadyDeployment(),
			withReplicaFailure(newReplicaSet("demo-111", "rs-uid-1"), "FailedCreate", marker),
		},
	})

	if _, err := h.waitFor("Failed", 15*time.Second, failedWithMarker(marker)); err != nil {
		t.Fatalf("wait for Failed: %v", err)
	}
}

func TestTrack_IgnoresOldReplicaSetReplicaFailure(t *testing.T) {
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newNotReadyDeployment()},
	})

	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}

	rsOld := newReplicaSet("demo-old", "rs-old")
	h.createObject(t, gvrReplicaSets, rsOld)
	if _, err := h.waitFor("AddedReplicaSet", 10*time.Second, addedNewReplicaSet(rsOld.Name)); err != nil {
		t.Fatalf("wait for AddedReplicaSet(old): %v", err)
	}

	updatedDep := newNotReadyDeployment()
	updatedDep.Generation = 2
	updatedDep.Status.ObservedGeneration = 2
	updatedDep.Spec.Template.Spec.Containers[0].Image = "busybox:2"
	h.updateDeployment(t, updatedDep)
	if _, err := h.waitFor("Status", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Status after deployment update: %v", err)
	}

	rsNew := newReplicaSet("demo-new", "rs-new")
	rsNew.Spec.Template.Spec.Containers[0].Image = "busybox:2"
	h.createObject(t, gvrReplicaSets, rsNew)
	if _, err := h.waitFor("AddedReplicaSet", 10*time.Second, addedNewReplicaSet(rsNew.Name)); err != nil {
		t.Fatalf("wait for AddedReplicaSet(new): %v", err)
	}

	const markerOld = "marker-old-rollout"
	h.updateReplicaSet(t, withReplicaFailure(rsOld.DeepCopy(), "FailedCreate", markerOld))
	if err := h.waitForNone("Failed", 2500*time.Millisecond, failedWithMarker(markerOld)); err != nil {
		t.Fatalf("previous rollout replicaset must not fail the current one: %v", err)
	}

	const markerNew = "marker-new-rollout"
	h.updateReplicaSet(t, withReplicaFailure(rsNew.DeepCopy(), "FailedCreate", markerNew))
	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(markerNew)); err != nil {
		t.Fatalf("wait for Failed: %v", err)
	}
}

func TestTrack_ReportsReplicaFailureOncePerIncarnation(t *testing.T) {
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

	const marker = "marker-dedupe"
	failed := withReplicaFailure(rs.DeepCopy(), "FailedCreate", marker)
	h.updateReplicaSet(t, failed)
	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(marker)); err != nil {
		t.Fatalf("wait for Failed: %v", err)
	}

	stillFailing := failed.DeepCopy()
	stillFailing.Status.ObservedGeneration = 2
	h.updateReplicaSet(t, stillFailing)
	if err := h.waitForNone("Failed", 2500*time.Millisecond, failedWithMarker(marker)); err != nil {
		t.Fatalf("unchanged failure condition must not be reported twice: %v", err)
	}
}

func TestTrack_ReportsReplicaFailureAgainAfterConditionCleared(t *testing.T) {
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

	const marker = "marker-rearm"
	h.updateReplicaSet(t, withReplicaFailure(rs.DeepCopy(), "FailedCreate", marker))
	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(marker)); err != nil {
		t.Fatalf("wait for first Failed: %v", err)
	}

	h.updateReplicaSet(t, rs.DeepCopy())
	h.updateReplicaSet(t, withReplicaFailure(rs.DeepCopy(), "FailedCreate", marker))
	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(marker)); err != nil {
		t.Fatalf("failure recurring after recovery must be reported again: %v", err)
	}
}

func TestTrack_IgnoresFailedDeleteReplicaFailure(t *testing.T) {
	const marker = "marker-failed-delete"
	rs := withReplicaFailure(newReplicaSet("demo-111", "rs-uid-1"), "FailedDelete", marker)
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newNotReadyDeployment(), rs},
	})

	if _, err := h.waitFor("AddedReplicaSet", 10*time.Second, addedNewReplicaSet(rs.Name)); err != nil {
		t.Fatalf("wait for AddedReplicaSet: %v", err)
	}

	if err := h.waitForNone("Failed", 2500*time.Millisecond, failedWithMarker(marker)); err != nil {
		t.Fatalf("FailedDelete must not fail the rollout: %v", err)
	}
}

func TestTrack_FailsOnReplicaFailureWithEventsDisabled(t *testing.T) {
	t.Setenv("KUBEDOG_DISABLE_EVENTS", "1")

	const marker = "marker-events-disabled"
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{
			newNotReadyDeployment(),
			withReplicaFailure(newReplicaSet("demo-111", "rs-uid-1"), "FailedCreate", marker),
		},
	})

	if _, err := h.waitFor("Failed", 15*time.Second, failedWithMarker(marker)); err != nil {
		t.Fatalf("replica failure detection must not depend on events: %v", err)
	}
}

func TestTrack_ReportsEventFailureAsCounted(t *testing.T) {
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newNotReadyDeployment()},
	})

	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}

	const marker = "marker-counted-event-failure"
	h.tracker.TestOnlyInjectResourceFailure(marker)

	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMode(marker, deployment.FailureModeCounted)); err != nil {
		t.Fatalf("event failure must remain subject to the error budget: %v", err)
	}
}

func TestTrack_FailsOnRecreatedReplicaSetWithSameNameNewUID(t *testing.T) {
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newNotReadyDeployment()},
	})

	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}

	rsOld := newReplicaSet("demo-111", "rs-uid-old")
	h.createObject(t, gvrReplicaSets, rsOld)
	if _, err := h.waitFor("AddedReplicaSet", 10*time.Second, addedNewReplicaSet(rsOld.Name)); err != nil {
		t.Fatalf("wait for AddedReplicaSet(old): %v", err)
	}

	h.deleteObject(t, gvrReplicaSets, rsOld.Name)

	const marker = "marker-recreated-rs"
	h.createObject(t, gvrReplicaSets, withReplicaFailure(newReplicaSet("demo-111", "rs-uid-new"), "FailedCreate", marker))

	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(marker)); err != nil {
		t.Fatalf("wait for Failed: %v", err)
	}
}

func TestTrack_StopsReportingReplicaFailuresAfterDeploymentDeletion(t *testing.T) {
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

	h.deleteObject(t, gvrDeployments, testDeploymentName)
	if _, err := h.waitFor("Status", 10*time.Second, deletionResetStatus); err != nil {
		t.Fatalf("wait for deletion status: %v", err)
	}

	const marker = "marker-after-deletion"
	h.updateReplicaSet(t, withReplicaFailure(rs.DeepCopy(), "FailedCreate", marker))
	if err := h.waitForNone("Failed", 2500*time.Millisecond, failedWithMarker(marker)); err != nil {
		t.Fatalf("replica failures must not be reported for a deleted deployment: %v", err)
	}
}

func addedNewReplicaSet(name string) func(observation) bool {
	return func(ev observation) bool {
		report, ok := ev.Data.(deployment.ReplicaSetAddedReport)
		if !ok {
			return false
		}
		return report.ReplicaSet.Name == name && report.ReplicaSet.IsNew
	}
}

// The zero-value status emitted on resource deletion is the only Status with
// StatusGeneration == 0, since NewDeploymentStatus always increments it first.
func deletionResetStatus(ev observation) bool {
	status, ok := ev.Data.(deployment.DeploymentStatus)
	if !ok {
		return false
	}
	return status.StatusGeneration == 0 && !status.IsFailed
}
