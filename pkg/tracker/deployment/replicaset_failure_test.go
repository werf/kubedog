package deployment

import "testing"

func TestReplicaSetFailureMode_whenFailedCreateMessageIsClassified(t *testing.T) {
	tests := []struct {
		name                 string
		message              string
		deploymentCanRecover bool
		want                 FailureMode
	}{
		{
			name:    "quota exhaustion is fatal",
			message: `pods "demo-111" is forbidden: exceeded quota: compute-quota`,
			want:    FailureModeFatal,
		},
		{
			name:                 "quota exhaustion is counted when rollout can release quota",
			message:              `pods "demo-111" is forbidden: exceeded quota: compute-quota`,
			deploymentCanRecover: true,
			want:                 FailureModeCounted,
		},
		{
			name:    "invalid pod specification is fatal",
			message: `Pod "demo-111" is invalid: spec.containers[0].resources.requests: Invalid value`,
			want:    FailureModeFatal,
		},
		{
			name:    "temporary API failure is counted",
			message: "failed calling create: temporary apiserver connection refused",
			want:    FailureModeCounted,
		},
		{
			name:    "admission webhook rejection is counted",
			message: `admission webhook "policy.example" denied the request`,
			want:    FailureModeCounted,
		},
		{
			name:    "RBAC rejection is counted",
			message: `pods is forbidden: User "system:serviceaccount:default:controller" cannot create resource "pods"`,
			want:    FailureModeCounted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := replicaSetFailureMode(tt.message, tt.deploymentCanRecover); got != tt.want {
				t.Fatalf("replicaSetFailureMode(%q, %t) = %v, want %v", tt.message, tt.deploymentCanRecover, got, tt.want)
			}
		})
	}
}
