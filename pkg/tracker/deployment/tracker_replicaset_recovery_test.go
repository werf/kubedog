package deployment_test

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/werf/kubedog/pkg/tracker/deployment"
)

func TestTrack_RecoversFromTransientReplicaFailure(t *testing.T) {
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newNotReadyDeployment()},
	})

	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}

	const marker = "marker-transient-apiserver-failure"
	rs := newReplicaSet("demo-111", "rs-uid-1")
	failed := withReplicaFailure(rs.DeepCopy(), "FailedCreate", marker)
	failed.Status.Conditions[0].Message = "temporary apiserver connection refused [" + marker + "]"
	h.createObject(t, gvrReplicaSets, failed)

	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMode(marker, deployment.FailureModeCounted)); err != nil {
		t.Fatalf("wait for counted failure: %v", err)
	}

	h.updateReplicaSet(t, rs.DeepCopy())
	h.updateDeployment(t, newReadyDeployment())
	if _, err := h.waitFor("Ready", 10*time.Second, nil); err != nil {
		t.Fatalf("transient failure must allow later readiness: %v", err)
	}
}

func TestTrack_DetectsFailureAfterSameNameReplicaSetReplacementUpdate(t *testing.T) {
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newNotReadyDeployment()},
	})

	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}

	oldReplicaSet := newReplicaSet("demo-111", "rs-uid-old")
	h.createObject(t, gvrReplicaSets, oldReplicaSet)
	if _, err := h.waitFor("AddedReplicaSet", 10*time.Second, addedNewReplicaSet(oldReplicaSet.Name)); err != nil {
		t.Fatalf("wait for old AddedReplicaSet: %v", err)
	}

	const marker = "marker-replacement-update"
	replacement := withReplicaFailure(oldReplicaSet.DeepCopy(), "FailedCreate", marker)
	replacement.UID = types.UID("rs-uid-new")
	h.updateReplicaSet(t, replacement)

	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(marker)); err != nil {
		t.Fatalf("same-name replacement delivered as Update must be tracked: %v", err)
	}
}

func TestTrack_ReportsReplicaFailureAfterSelectorReentry(t *testing.T) {
	h := newHarness(t, harnessConfig{
		dynamicSeed: []runtime.Object{newNotReadyDeployment()},
	})

	if _, err := h.waitFor("Added", 10*time.Second, nil); err != nil {
		t.Fatalf("wait for Added: %v", err)
	}

	const marker = "marker-selector-reentry"
	rs := withReplicaFailure(newReplicaSet("demo-111", "rs-uid-1"), "FailedCreate", marker)
	h.createObject(t, gvrReplicaSets, rs)
	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(marker)); err != nil {
		t.Fatalf("wait for initial failure: %v", err)
	}

	h.tracker.TestOnlyInjectReplicaSetUnselected(rs.DeepCopy())
	h.tracker.TestOnlyInjectReplicaSetAdded(rs.DeepCopy())
	if _, err := h.waitFor("Failed", 10*time.Second, failedWithMarker(marker)); err != nil {
		t.Fatalf("same UID must become reportable after selector reentry: %v", err)
	}
}
