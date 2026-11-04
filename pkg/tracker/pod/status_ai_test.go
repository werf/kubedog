//go:build ai_tests

package pod

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newPendingPod(conditions ...corev1.PodCondition) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "example", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app"}},
		},
		Status: corev1.PodStatus{
			Phase:      corev1.PodPending,
			Conditions: conditions,
		},
	}
}

// An unschedulable pod never starts its containers, so the tracker must report it instead of
// waiting for readiness until the timeout.
func TestNewPodStatusReportsUnschedulablePod(t *testing.T) {
	pod := newPendingPod(corev1.PodCondition{
		Type:    corev1.PodScheduled,
		Status:  corev1.ConditionFalse,
		Reason:  corev1.PodReasonUnschedulable,
		Message: "0/1 nodes are available: 1 Insufficient memory.",
	})

	status := NewPodStatus(pod, 1, []string{"app"}, false, "")

	require.Len(t, status.ContainersErrors, 1)
	assert.Equal(t, "", status.ContainersErrors[0].ContainerName)
	assert.Equal(t, "Unschedulable: 0/1 nodes are available: 1 Insufficient memory.", status.ContainersErrors[0].Message)
	assert.False(t, status.IsFailed, "unschedulable pod may still be scheduled later, e.g. by cluster autoscaler")
}

func TestNewPodStatusIgnoresScheduledPod(t *testing.T) {
	pod := newPendingPod(corev1.PodCondition{
		Type:   corev1.PodScheduled,
		Status: corev1.ConditionTrue,
	})

	status := NewPodStatus(pod, 1, []string{"app"}, false, "")

	assert.Empty(t, status.ContainersErrors)
}

func TestNewPodStatusIgnoresPendingSchedulingWithOtherReason(t *testing.T) {
	pod := newPendingPod(corev1.PodCondition{
		Type:   corev1.PodScheduled,
		Status: corev1.ConditionFalse,
		Reason: corev1.PodReasonSchedulingGated,
	})

	status := NewPodStatus(pod, 1, []string{"app"}, false, "")

	assert.Empty(t, status.ContainersErrors)
}
