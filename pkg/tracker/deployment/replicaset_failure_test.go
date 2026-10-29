package deployment

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/werf/kubedog/pkg/tracker"
)

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

func TestQuotaStillExceeded_whenQuotaStatusChanged(t *testing.T) {
	const message = `pods "demo-111" is forbidden: exceeded quota: compute-quota, requested: limits.cpu=750m,limits.memory=1Gi, used: limits.cpu=3600m,limits.memory=3Gi, limited: limits.cpu=4,limits.memory=4Gi`

	tests := []struct {
		name    string
		message string
		used    corev1.ResourceList
		want    bool
		wantErr bool
	}{
		{
			name:    "one requested resource still exceeds the quota",
			message: message,
			used:    corev1.ResourceList{corev1.ResourceLimitsCPU: resource.MustParse("3600m"), corev1.ResourceLimitsMemory: resource.MustParse("1Gi")},
			want:    true,
		},
		{
			name:    "quota has room for every requested resource",
			message: message,
			used:    corev1.ResourceList{corev1.ResourceLimitsCPU: resource.MustParse("3250m"), corev1.ResourceLimitsMemory: resource.MustParse("3Gi")},
			want:    false,
		},
		{
			// Anyone allowed to write the ReplicaSet status controls the message, and parsing
			// such a quantity takes the arbitrary-precision arithmetic minutes.
			name:    "requested quantity is out of the range the quota admission plugin prints",
			message: `pods "demo-111" is forbidden: exceeded quota: compute-quota, requested: limits.cpu=1e-1000000000, used: limits.cpu=0, limited: limits.cpu=4`,
			wantErr: true,
		},
		{
			name:    "message is not a quota admission failure",
			message: `pods "demo-111" is forbidden: exceeded quota: compute-quota`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			quota := &corev1.ResourceQuota{
				ObjectMeta: metav1.ObjectMeta{Name: "compute-quota", Namespace: "default"},
				Status: corev1.ResourceQuotaStatus{
					Hard: corev1.ResourceList{corev1.ResourceLimitsCPU: resource.MustParse("4"), corev1.ResourceLimitsMemory: resource.MustParse("4Gi")},
					Used: tt.used,
				},
			}
			d := &Tracker{Tracker: tracker.Tracker{Kube: k8sfake.NewSimpleClientset(quota), Namespace: "default"}}

			got, err := d.quotaStillExceeded(context.Background(), tt.message)
			if (err != nil) != tt.wantErr {
				t.Fatalf("quotaStillExceeded() error = %v, wantErr %t", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("quotaStillExceeded() = %t, want %t", got, tt.want)
			}
		})
	}
}
