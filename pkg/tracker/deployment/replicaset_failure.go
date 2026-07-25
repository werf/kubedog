package deployment

import (
	"context"
	"fmt"
	"strings"

	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/utils"
)

func (d *Tracker) clearReplicaSetFailure(replicaSetUID types.UID) {
	if replicaSetUID == "" || d.failedFromReplicaSetUID != replicaSetUID {
		return
	}

	d.failedFromReplicaSetUID = ""

	if d.State == tracker.ResourceFailed {
		d.State = tracker.ResourceAdded
	}
}

func (d *Tracker) rearmReplicaSetFailure(rs *appsv1.ReplicaSet) {
	if _, failed := replicaSetFailure(rs); failed {
		return
	}

	delete(d.reportedReplicaSetFailures, rs.UID)
	d.clearReplicaSetFailure(rs.UID)
}

func (d *Tracker) isStaleReplicaSet(rs *appsv1.ReplicaSet) bool {
	_, deleted := d.deletedReplicaSetUIDs[rs.UID]
	return deleted
}

func (d *Tracker) handleNewReplicaSetFailure(ctx context.Context) error {
	if d.lastObject == nil || !d.replicaSetsSynced {
		return nil
	}

	newReplicaSet, err := utils.FindNewReplicaSet(d.lastObject, lo.Values(d.knownReplicaSets))
	if err != nil {
		return err
	}
	if newReplicaSet == nil {
		return nil
	}

	failure, failed := replicaSetFailure(newReplicaSet)
	if !failed {
		return nil
	}

	if deploymentStatusReady(d.lastObject) {
		delete(d.reportedReplicaSetFailures, newReplicaSet.UID)
		d.clearReplicaSetFailure(newReplicaSet.UID)
		return nil
	}

	mode := replicaSetFailureMode(failure.Message, d.deploymentCanReleaseQuota(newReplicaSet))
	// ReplicaSet controllers retain FailedCreate until a successful retry. Keep
	// the first classification until that condition clears so an old condition
	// cannot become fatal merely because an older ReplicaSet finished scaling down.
	if _, reported := d.reportedReplicaSetFailures[newReplicaSet.UID]; reported {
		return nil
	}
	d.reportedReplicaSetFailures[newReplicaSet.UID] = struct{}{}

	return d.handleResourceFailure(ctx, resourceFailure{
		reason:            fmt.Sprintf("%s: %s", failure.Reason, failure.Message),
		fromReplicaSetUID: newReplicaSet.UID,
		mode:              mode,
	})
}

func replicaSetFailureMode(message string, deploymentCanRecover bool) FailureMode {
	message = strings.ToLower(message)
	if strings.Contains(message, " is invalid:") ||
		(strings.Contains(message, "exceeded quota:") && !deploymentCanRecover) {
		return FailureModeFatal
	}

	return FailureModeCounted
}

func (d *Tracker) deploymentCanReleaseQuota(newReplicaSet *appsv1.ReplicaSet) bool {
	oldReplicaSetsHaveDesiredReplicas := false
	allDesiredReplicas := *newReplicaSet.Spec.Replicas
	for _, rs := range d.knownReplicaSets {
		if rs.UID == newReplicaSet.UID || !metav1.IsControlledBy(rs, d.lastObject) {
			continue
		}

		desiredReplicas := *rs.Spec.Replicas
		if rs.Status.TerminatingReplicas != nil && *rs.Status.TerminatingReplicas > 0 {
			return true
		}
		if rs.Status.Replicas > desiredReplicas {
			return true
		}

		allDesiredReplicas += desiredReplicas
		oldReplicaSetsHaveDesiredReplicas = oldReplicaSetsHaveDesiredReplicas || desiredReplicas > 0
	}
	if !oldReplicaSetsHaveDesiredReplicas {
		return false
	}

	if d.lastObject.Spec.Strategy.Type == appsv1.RecreateDeploymentStrategyType {
		return true
	}
	if d.lastObject.Spec.Strategy.Type != appsv1.RollingUpdateDeploymentStrategyType ||
		d.lastObject.Spec.Strategy.RollingUpdate == nil {
		return false
	}

	desiredReplicas := *d.lastObject.Spec.Replicas
	rollingUpdate := d.lastObject.Spec.Strategy.RollingUpdate
	maxSurge, err := intstr.GetScaledValueFromIntOrPercent(
		intstr.ValueOrDefault(rollingUpdate.MaxSurge, intstr.FromInt32(0)), int(desiredReplicas), true,
	)
	if err != nil {
		return true
	}
	maxUnavailable, err := intstr.GetScaledValueFromIntOrPercent(
		intstr.ValueOrDefault(rollingUpdate.MaxUnavailable, intstr.FromInt32(0)), int(desiredReplicas), false,
	)
	if err != nil {
		return true
	}
	if maxSurge == 0 && maxUnavailable == 0 {
		maxUnavailable = 1
	}
	if int32(maxUnavailable) > desiredReplicas {
		maxUnavailable = int(desiredReplicas)
	}

	minimumAvailable := desiredReplicas - int32(maxUnavailable)
	newUnavailable := *newReplicaSet.Spec.Replicas - newReplicaSet.Status.AvailableReplicas
	return allDesiredReplicas-minimumAvailable-newUnavailable > 0
}

func replicaSetFailure(rs *appsv1.ReplicaSet) (appsv1.ReplicaSetCondition, bool) {
	for _, condition := range rs.Status.Conditions {
		if condition.Type != appsv1.ReplicaSetReplicaFailure ||
			condition.Status != corev1.ConditionTrue ||
			condition.Reason != replicaSetFailedCreateReason {
			continue
		}

		return condition, true
	}

	return appsv1.ReplicaSetCondition{}, false
}
