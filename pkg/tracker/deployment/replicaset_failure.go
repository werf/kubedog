package deployment

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/utils"
)

const (
	rolloutAPIRequestTimeout         = 10 * time.Second
	defaultQuotaDeadlockConfirmation = 10 * time.Second
)

var (
	exceededQuotaPattern = regexp.MustCompile(`exceeded quota: ([^,\s]+), requested: (\S+), used: \S+, limited: \S+`)
	// boundedQuantityPattern admits the canonical quantities the quota admission plugin prints
	// and keeps their exponent small: parsing a huge one takes minutes.
	boundedQuantityPattern = regexp.MustCompile(`^[0-9]{1,30}(\.[0-9]{1,30})?([numkMGTPE]|[KMGTPE]i|[eE][+-]?[0-9]{1,2})?$`)
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

	d.forgetReplicaSetFailure(rs.UID)
	d.clearReplicaSetFailure(rs.UID)
}

func (d *Tracker) forgetReplicaSetFailure(replicaSetUID types.UID) {
	delete(d.reportedReplicaSetFailures, replicaSetUID)
	delete(d.quotaDeadlockSince, replicaSetUID)
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
		d.forgetReplicaSetFailure(newReplicaSet.UID)
		d.clearReplicaSetFailure(newReplicaSet.UID)
		return nil
	}

	// ReplicaSet controllers retain FailedCreate until a successful retry. Keep
	// the first classification until that condition clears so an old condition
	// cannot become fatal merely because an older ReplicaSet finished scaling down.
	if _, reported := d.reportedReplicaSetFailures[newReplicaSet.UID]; reported {
		return nil
	}

	deploymentCanRecover := true
	if isQuotaFailure(failure.Message) {
		var decided bool
		deploymentCanRecover, decided = d.confirmQuotaDeadlock(ctx, newReplicaSet, failure)
		if !decided {
			return nil
		}
	}

	d.reportedReplicaSetFailures[newReplicaSet.UID] = struct{}{}
	mode := replicaSetFailureMode(failure.Message, deploymentCanRecover)

	return d.handleResourceFailure(ctx, resourceFailure{
		reason:            fmt.Sprintf("%s: %s", failure.Reason, failure.Message),
		fromReplicaSetUID: newReplicaSet.UID,
		mode:              mode,
	})
}

func replicaSetFailureMode(message string, deploymentCanRecover bool) FailureMode {
	if strings.Contains(strings.ToLower(message), " is invalid:") ||
		(isQuotaFailure(message) && !deploymentCanRecover) {
		return FailureModeFatal
	}

	return FailureModeCounted
}

func isQuotaFailure(message string) bool {
	return strings.Contains(strings.ToLower(message), "exceeded quota:")
}

// confirmQuotaDeadlock reports a deadlock only once it has held for a while: the checks read
// different objects at different moments, and quota usage drops only when the quota controller
// catches up with deleted pods. Until then the failure stays undecided.
func (d *Tracker) confirmQuotaDeadlock(ctx context.Context, newReplicaSet *appsv1.ReplicaSet, failure appsv1.ReplicaSetCondition) (deploymentCanRecover, decided bool) {
	if d.deploymentCanReleaseQuota(ctx, newReplicaSet, failure) {
		return true, true
	}

	since, pending := d.quotaDeadlockSince[newReplicaSet.UID]
	if !pending {
		d.quotaDeadlockSince[newReplicaSet.UID] = time.Now()
		time.AfterFunc(d.quotaDeadlockConfirmation, func() {
			select {
			case d.quotaDeadlockRecheck <- struct{}{}:
			default:
			}
		})

		return false, false
	}

	return false, time.Since(since) >= d.quotaDeadlockConfirmation
}

// deploymentCanReleaseQuota errs towards true: false fails the deploy immediately.
func (d *Tracker) deploymentCanReleaseQuota(ctx context.Context, newReplicaSet *appsv1.ReplicaSet, failure appsv1.ReplicaSetCondition) bool {
	if d.rolloutCanReleaseQuota(newReplicaSet, newReplicaSet.Status.Replicas) {
		return true
	}

	// The status that brings the condition counts only the pods listed before the failed
	// sync created any, and cached objects do not show terminating pods.
	activeNewPods, releasing, err := d.inspectRolloutPods(ctx, newReplicaSet)
	if err != nil || releasing || activeNewPods >= *newReplicaSet.Spec.Replicas ||
		d.rolloutCanReleaseQuota(newReplicaSet, activeNewPods) {
		return true
	}

	// The ReplicaSet controller keeps the condition across its backed-off retries, so the
	// shortage it reports may be over already.
	exceeded, err := d.quotaStillExceeded(ctx, failure.Message)
	return err != nil || !exceeded
}

// rolloutCanReleaseQuota assumes every existing pod of the new ReplicaSet becomes available
// and checks whether the Deployment controller would then scale an old ReplicaSet down.
func (d *Tracker) rolloutCanReleaseQuota(newReplicaSet *appsv1.ReplicaSet, activeNewPods int32) bool {
	oldReplicaSetsHaveDesiredReplicas := false
	var oldDesiredReplicas int32
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

		oldDesiredReplicas += desiredReplicas
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

	// The Deployment controller scales old ReplicaSets down by at most
	// sum(old spec) + new available - minimum available.
	minimumAvailable := desiredReplicas - int32(maxUnavailable)
	return oldDesiredReplicas+activeNewPods > minimumAvailable
}

// inspectRolloutPods counts the active pods of the new ReplicaSet and reports whether any pod
// of the rollout is terminating or about to be scaled down, both of which release quota.
func (d *Tracker) inspectRolloutPods(ctx context.Context, newReplicaSet *appsv1.ReplicaSet) (activeNewPods int32, releasing bool, err error) {
	selector, err := metav1.LabelSelectorAsSelector(d.lastObject.Spec.Selector)
	if err != nil {
		return 0, false, err
	}

	listCtx, cancel := context.WithTimeout(ctx, rolloutAPIRequestTimeout)
	defer cancel()

	// An unset resource version makes a quorum read, which sees the pods created
	// before the ReplicaSet status carrying the condition was written.
	pods, err := d.Kube.CoreV1().Pods(d.Namespace).List(listCtx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return 0, false, err
	}

	desiredByReplicaSet := make(map[types.UID]int32)
	for _, rs := range d.knownReplicaSets {
		if metav1.IsControlledBy(rs, d.lastObject) {
			desiredByReplicaSet[rs.UID] = *rs.Spec.Replicas
		}
	}

	activeByReplicaSet := make(map[types.UID]int32)
	for i := range pods.Items {
		pod := &pods.Items[i]
		owner := metav1.GetControllerOf(pod)
		if owner == nil {
			continue
		}
		if _, owned := desiredByReplicaSet[owner.UID]; !owned {
			continue
		}
		if pod.DeletionTimestamp != nil {
			return 0, true, nil
		}
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}

		activeByReplicaSet[owner.UID]++
	}

	for replicaSetUID, active := range activeByReplicaSet {
		if replicaSetUID != newReplicaSet.UID && active > desiredByReplicaSet[replicaSetUID] {
			return 0, true, nil
		}
	}

	return activeByReplicaSet[newReplicaSet.UID], false, nil
}

// quotaStillExceeded parses the message of the ResourceQuota admission plugin and reports
// whether the named quota still has no room for the request.
func (d *Tracker) quotaStillExceeded(ctx context.Context, message string) (bool, error) {
	match := exceededQuotaPattern.FindStringSubmatch(message)
	if match == nil {
		return false, fmt.Errorf("parse quota failure %q", message)
	}

	requested := make(corev1.ResourceList)
	for _, item := range strings.Split(match[2], ",") {
		name, value, found := strings.Cut(item, "=")
		if !found {
			return false, fmt.Errorf("parse requested resource %q", item)
		}

		if !boundedQuantityPattern.MatchString(value) {
			return false, fmt.Errorf("parse requested quantity %q", item)
		}

		quantity, err := resource.ParseQuantity(value)
		if err != nil {
			return false, fmt.Errorf("parse requested quantity %q: %w", item, err)
		}

		requested[corev1.ResourceName(name)] = quantity
	}

	getCtx, cancel := context.WithTimeout(ctx, rolloutAPIRequestTimeout)
	defer cancel()

	quota, err := d.Kube.CoreV1().ResourceQuotas(d.Namespace).Get(getCtx, match[1], metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("get resource quota %q: %w", match[1], err)
	}

	for name, quantity := range requested {
		hard, limited := quota.Status.Hard[name]
		if !limited {
			continue
		}

		used := quota.Status.Used[name]
		used.Add(quantity)
		if used.Cmp(hard) > 0 {
			return true, nil
		}
	}

	return false, nil
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
