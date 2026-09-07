package dyntracker

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/werf/kubedog/pkg/dyntracker/statestore"
	"github.com/werf/kubedog/pkg/tracker/deployment"
)

func TestHandleDeploymentStatus_KeepsCountedFailureWithinAllowance(t *testing.T) {
	taskState := statestore.NewReadinessTaskState(
		"demo",
		"default",
		schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
		statestore.ReadinessTaskStateOptions{TotalAllowFailuresCount: 1},
	)
	status := deployment.DeploymentStatus{
		IsFailed:     true,
		FailedReason: "counted event failure",
		FailureMode:  deployment.FailureModeCounted,
	}

	(&DynamicReadinessTracker{}).handleDeploymentStatus(&status, taskState)

	if got := taskState.Status(); got != statestore.ReadinessTaskStatusProgressing {
		t.Fatalf("counted failure within allowance must keep tracking progressing, got %s", got)
	}
}

func TestHandleDeploymentStatus_FailsOnFatalFailureInLegacyFailMode(t *testing.T) {
	taskState := statestore.NewReadinessTaskState(
		"demo",
		"default",
		schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
		statestore.ReadinessTaskStateOptions{
			FailMode:                statestore.LegacyHopeUntilEndOfDeployProcess,
			TotalAllowFailuresCount: 1,
		},
	)
	status := deployment.DeploymentStatus{
		IsFailed:     true,
		FailedReason: `pods "demo-111" is forbidden: exceeded quota: compute-quota`,
		FailureMode:  deployment.FailureModeFatal,
	}

	(&DynamicReadinessTracker{}).handleDeploymentStatus(&status, taskState)

	if got := taskState.Status(); got != statestore.ReadinessTaskStatusFailed {
		t.Fatalf("fatal failure must fail tracking regardless of the allowance, got %s", got)
	}
}

func TestHandleDeploymentStatus_KeepsFatalFailureIgnoredInIgnoreFailMode(t *testing.T) {
	taskState := statestore.NewReadinessTaskState(
		"demo",
		"default",
		schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
		statestore.ReadinessTaskStateOptions{
			FailMode:                statestore.IgnoreAndContinueDeployProcess,
			TotalAllowFailuresCount: 0,
		},
	)
	status := deployment.DeploymentStatus{
		IsFailed:     true,
		FailedReason: `pods "demo-111" is forbidden: exceeded quota: compute-quota`,
		FailureMode:  deployment.FailureModeFatal,
	}

	(&DynamicReadinessTracker{}).handleDeploymentStatus(&status, taskState)

	resourceState := taskState.ResourceState(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind())

	var resourceStatus statestore.ResourceStatus
	resourceState.RTransaction(func(rs *statestore.ResourceState) {
		resourceStatus = rs.Status()
	})

	if resourceStatus != statestore.ResourceStatusFailed {
		t.Fatalf("fatal failure must be recorded on the resource state, got %s", resourceStatus)
	}

	if got := taskState.Status(); got != statestore.ReadinessTaskStatusProgressing {
		t.Fatalf("ignore fail mode must keep tracking progressing on a fatal failure, got %s", got)
	}
}
