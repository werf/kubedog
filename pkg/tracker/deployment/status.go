package deployment

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"

	"github.com/werf/kubedog/pkg/tracker/indicators"
	"github.com/werf/kubedog/pkg/tracker/pod"
)

// FailureMode qualifies a reported failure: a counted one may be transient and spends the
// consumer's error budget, while a fatal one is durable and bypasses it.
type FailureMode uint8

const (
	// FailureModeCounted is a recoverable failure charged against the consumer's error budget.
	FailureModeCounted FailureMode = iota
	// FailureModeFatal is a rollout-wide failure that cannot recover without an external change.
	FailureModeFatal
)

type DeploymentStatus struct {
	appsv1.DeploymentStatus

	StatusGeneration uint64

	ReplicasIndicator  *indicators.Int32EqualConditionIndicator
	UpToDateIndicator  *indicators.Int32EqualConditionIndicator
	AvailableIndicator *indicators.Int32EqualConditionIndicator

	WaitingForMessages []string

	IsReady      bool
	IsFailed     bool
	FailedReason string
	FailureMode  FailureMode

	Pods map[string]pod.PodStatus
	// New Pod belongs to the new ReplicaSet of the Deployment,
	// i.e. actual up-to-date Pod of the Deployment
	NewPodsNames []string
}

func NewDeploymentStatus(object *appsv1.Deployment, statusGeneration uint64, isTrackerFailed bool, trackerFailedReason string, podsStatuses map[string]pod.PodStatus, newPodsNames []string) DeploymentStatus {
	res := DeploymentStatus{
		StatusGeneration: statusGeneration,
		DeploymentStatus: object.Status,
		Pods:             make(map[string]pod.PodStatus),
		NewPodsNames:     newPodsNames,
	}

processingPodsStatuses:
	for k, v := range podsStatuses {
		res.Pods[k] = v

		for _, newPodName := range newPodsNames {
			if newPodName == k {
				if v.StatusIndicator != nil {
					// New Pod should be Running
					v.StatusIndicator.TargetValue = "Running"
				}
				continue processingPodsStatuses
			}
		}

		if v.StatusIndicator != nil {
			// Old Pod should gone
			v.StatusIndicator.TargetValue = ""
		}
	}

	res.IsReady = false

	if object.Status.ObservedGeneration >= object.Generation {
		if object.Spec.Replicas == nil {
			return res
		}

		res.ReplicasIndicator = &indicators.Int32EqualConditionIndicator{
			Value:       object.Status.Replicas,
			TargetValue: *object.Spec.Replicas,
		}
		res.UpToDateIndicator = &indicators.Int32EqualConditionIndicator{
			Value:       object.Status.UpdatedReplicas,
			TargetValue: *object.Spec.Replicas,
		}
		res.AvailableIndicator = &indicators.Int32EqualConditionIndicator{
			Value:       object.Status.AvailableReplicas,
			TargetValue: *object.Spec.Replicas,
		}

		res.IsReady = deploymentStatusReady(object)
		if object.Status.UpdatedReplicas != *object.Spec.Replicas {
			res.WaitingForMessages = append(res.WaitingForMessages, fmt.Sprintf("up-to-date %d->%d", object.Status.UpdatedReplicas, *object.Spec.Replicas))
		}
		if object.Status.Replicas != *object.Spec.Replicas {
			res.WaitingForMessages = append(res.WaitingForMessages, fmt.Sprintf("replicas %d->%d", object.Status.Replicas, *object.Spec.Replicas))
		}
		if object.Status.AvailableReplicas != *object.Spec.Replicas {
			res.WaitingForMessages = append(res.WaitingForMessages, fmt.Sprintf("available %d->%d", object.Status.AvailableReplicas, *object.Spec.Replicas))
		}
	} else {
		res.WaitingForMessages = append(res.WaitingForMessages, fmt.Sprintf("observed generation %d should be >= %d", object.Status.ObservedGeneration, object.Generation))
	}

	if !res.IsReady && !res.IsFailed {
		res.IsFailed = isTrackerFailed
		res.FailedReason = trackerFailedReason
	}

	return res
}

// deploymentStatusReady reports whether the Deployment controller has observed the
// current spec and reports every replica up-to-date, present and available.
func deploymentStatusReady(object *appsv1.Deployment) bool {
	if object.Status.ObservedGeneration < object.Generation || object.Spec.Replicas == nil {
		return false
	}

	return object.Status.UpdatedReplicas == *object.Spec.Replicas &&
		object.Status.Replicas == *object.Spec.Replicas &&
		object.Status.AvailableReplicas == *object.Spec.Replicas
}
