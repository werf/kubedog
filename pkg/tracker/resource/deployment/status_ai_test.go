//go:build ai_tests

package deployment

import (
	"testing"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"

	legacy "github.com/werf/kubedog/pkg/tracker/deployment"
)

func newDeployment(generation, observedGeneration int64, replicas *int32, status appsv1.DeploymentStatus) *appsv1.Deployment {
	object := &appsv1.Deployment{
		Spec:   appsv1.DeploymentSpec{Replicas: replicas},
		Status: status,
	}
	object.Generation = generation
	object.Status.ObservedGeneration = observedGeneration

	return object
}

func TestAI_EvaluateMatchesLegacyStatus(t *testing.T) {
	tests := []struct {
		name           string
		object         *appsv1.Deployment
		wantReady      bool
		wantWaitingFor []string
	}{
		{
			name:      "rolled out",
			object:    newDeployment(2, 2, int32Ptr(3), appsv1.DeploymentStatus{Replicas: 3, UpdatedReplicas: 3, AvailableReplicas: 3}),
			wantReady: true,
		},
		{
			name:           "generation not observed",
			object:         newDeployment(3, 2, int32Ptr(3), appsv1.DeploymentStatus{Replicas: 3, UpdatedReplicas: 3, AvailableReplicas: 3}),
			wantWaitingFor: []string{"observed generation 2 should be >= 3"},
		},
		{
			name:   "replicas not set",
			object: newDeployment(1, 1, nil, appsv1.DeploymentStatus{}),
		},
		{
			name:           "rolling out",
			object:         newDeployment(2, 2, int32Ptr(3), appsv1.DeploymentStatus{Replicas: 4, UpdatedReplicas: 1, AvailableReplicas: 3}),
			wantWaitingFor: []string{"up-to-date 1->3", "replicas 4->3"},
		},
		{
			name:           "scaled to zero",
			object:         newDeployment(1, 1, int32Ptr(0), appsv1.DeploymentStatus{Replicas: 1, AvailableReplicas: 1}),
			wantWaitingFor: []string{"replicas 1->0", "available 1->0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ready, waitingFor := Evaluate(tt.object)
			assert.Equal(t, tt.wantReady, ready)
			assert.Equal(t, tt.wantWaitingFor, waitingFor)

			legacyStatus := legacy.NewDeploymentStatus(tt.object, 1, false, "", nil, nil)
			assert.Equal(t, legacyStatus.IsReady, ready, "readiness differs from the legacy tracker")
			assert.Equal(t, legacyStatus.WaitingForMessages, waitingFor, "waiting messages differ from the legacy tracker")
		})
	}
}

func TestAI_LegacyStatusKeepsFailureWithoutObject(t *testing.T) {
	status := legacyStatus(Status{Failed: true, FailedReason: "boom"})

	assert.True(t, status.IsFailed)
	assert.Equal(t, "boom", status.FailedReason)
	assert.Nil(t, status.ReplicasIndicator)
}

func TestAI_LegacyStatusBuildsIndicators(t *testing.T) {
	object := newDeployment(1, 1, int32Ptr(2), appsv1.DeploymentStatus{Replicas: 2, UpdatedReplicas: 1, AvailableReplicas: 1})

	status := legacyStatus(Status{Generation: 7, Object: object, Failed: true, FailedReason: "boom"})

	assert.Equal(t, uint64(7), status.StatusGeneration)
	assert.False(t, status.IsReady)
	assert.True(t, status.IsFailed)
	assert.Equal(t, "boom", status.FailedReason)
	if assert.NotNil(t, status.ReplicasIndicator) {
		assert.Equal(t, int32(2), status.ReplicasIndicator.TargetValue)
	}
	if assert.NotNil(t, status.UpToDateIndicator) {
		assert.Equal(t, int32(1), status.UpToDateIndicator.Value)
	}
}

func int32Ptr(v int32) *int32 {
	return &v
}
