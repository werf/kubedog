package deployment

import (
	"fmt"

	"github.com/flant/kubedog/pkg/tracker"
	"github.com/flant/kubedog/pkg/tracker/pod"
	"github.com/flant/kubedog/pkg/utils"

	extensions "k8s.io/api/extensions/v1beta1"
)

type DeploymentReadyIndicator struct {
	OverallReplicasIndicator    tracker.Int32EqualConditionIndicator
	UpdatedReplicasIndicator    tracker.Int32EqualConditionIndicator
	AvailableReplicasIndicator  tracker.Int32EqualConditionIndicator
	OldReplicasIndicator        tracker.Int32EqualConditionIndicator
	ObservedGenerationIndicator tracker.Int64GreaterOrEqualConditionIndicator

	IsReady bool
}

type DeploymentStatus struct {
	extensions.DeploymentStatus
	ReadyIndicator DeploymentReadyIndicator

	Pods map[string]pod.PodStatus

	IsFailed     bool
	FailedReason string
}

func NewDeploymentStatus(readyIndicator DeploymentReadyIndicator, isFailed bool, failedReason string, kubeSpec extensions.DeploymentSpec, kubeStatus extensions.DeploymentStatus, podsStatuses map[string]pod.PodStatus) DeploymentStatus {
	res := DeploymentStatus{
		DeploymentStatus: kubeStatus,
		ReadyIndicator:   readyIndicator,
		Pods:             make(map[string]pod.PodStatus),
		IsFailed:         isFailed,
		FailedReason:     failedReason,
	}
	for k, v := range podsStatuses {
		res.Pods[k] = v
	}
	return res
}

// GetDeploymentReadyIndicator considers a deployment to be complete once all of its desired replicas
// are updated and available, and no old pods are running.
func GetDeploymentReadyIndicator(deployment *extensions.Deployment, newStatus *extensions.DeploymentStatus) DeploymentReadyIndicator {
	oldStatus := deployment.Status

	res := DeploymentReadyIndicator{IsReady: true}

	res.OverallReplicasIndicator.Value = newStatus.Replicas
	res.OverallReplicasIndicator.PrevValue = oldStatus.Replicas
	res.OverallReplicasIndicator.TargetValue = *(deployment.Spec.Replicas)
	res.OverallReplicasIndicator.IsReadyConditionSatisfied = (res.OverallReplicasIndicator.Value == res.OverallReplicasIndicator.TargetValue)
	res.OverallReplicasIndicator.IsProgressing = (res.OverallReplicasIndicator.Value > res.OverallReplicasIndicator.PrevValue)
	res.IsReady = res.IsReady && res.OverallReplicasIndicator.IsReadyConditionSatisfied

	res.UpdatedReplicasIndicator.Value = newStatus.UpdatedReplicas
	res.UpdatedReplicasIndicator.PrevValue = oldStatus.UpdatedReplicas
	res.UpdatedReplicasIndicator.TargetValue = *(deployment.Spec.Replicas)
	res.UpdatedReplicasIndicator.IsReadyConditionSatisfied = (res.UpdatedReplicasIndicator.Value == res.UpdatedReplicasIndicator.TargetValue)
	res.UpdatedReplicasIndicator.IsProgressing = (res.UpdatedReplicasIndicator.Value > res.UpdatedReplicasIndicator.PrevValue)
	res.IsReady = res.IsReady && res.UpdatedReplicasIndicator.IsReadyConditionSatisfied

	res.AvailableReplicasIndicator.Value = newStatus.AvailableReplicas
	res.AvailableReplicasIndicator.PrevValue = oldStatus.AvailableReplicas
	res.AvailableReplicasIndicator.TargetValue = *(deployment.Spec.Replicas)
	res.AvailableReplicasIndicator.IsReadyConditionSatisfied = (res.AvailableReplicasIndicator.Value == res.AvailableReplicasIndicator.TargetValue)
	res.AvailableReplicasIndicator.IsProgressing = (res.AvailableReplicasIndicator.Value > res.AvailableReplicasIndicator.PrevValue)
	res.IsReady = res.IsReady && res.AvailableReplicasIndicator.IsReadyConditionSatisfied

	// Old replicas that need to be scaled down
	res.OldReplicasIndicator.Value = newStatus.Replicas - newStatus.UpdatedReplicas
	res.OldReplicasIndicator.PrevValue = oldStatus.Replicas - oldStatus.UpdatedReplicas
	res.OldReplicasIndicator.TargetValue = 0
	res.OldReplicasIndicator.IsReadyConditionSatisfied = (res.AvailableReplicasIndicator.Value == res.AvailableReplicasIndicator.TargetValue)
	res.OldReplicasIndicator.IsProgressing = (res.OldReplicasIndicator.Value < res.OldReplicasIndicator.PrevValue)
	res.IsReady = res.IsReady && res.OldReplicasIndicator.IsReadyConditionSatisfied

	res.ObservedGenerationIndicator.Value = newStatus.ObservedGeneration
	res.ObservedGenerationIndicator.PrevValue = oldStatus.ObservedGeneration
	res.ObservedGenerationIndicator.TargetValue = deployment.Generation
	res.ObservedGenerationIndicator.IsReadyConditionSatisfied = (res.ObservedGenerationIndicator.Value >= res.ObservedGenerationIndicator.TargetValue)
	res.ObservedGenerationIndicator.IsProgressing = (res.ObservedGenerationIndicator.Value > res.ObservedGenerationIndicator.PrevValue)
	res.IsReady = res.IsReady && res.ObservedGenerationIndicator.IsReadyConditionSatisfied

	return res
}

// Status returns a message describing deployment status, and a bool value indicating if the status is considered done.
func DeploymentRolloutStatus(deployment *extensions.Deployment, revision int64) (string, bool, error) {
	if revision > 0 {
		deploymentRev, err := utils.Revision(deployment)
		if err != nil {
			return "", false, fmt.Errorf("cannot get the revision of deployment %q: %v", deployment.Name, err)
		}
		if revision != deploymentRev {
			return "", false, fmt.Errorf("desired revision (%d) is different from the running revision (%d)", revision, deploymentRev)
		}
	}
	if deployment.Generation <= deployment.Status.ObservedGeneration {
		cond := utils.GetDeploymentCondition(deployment.Status, extensions.DeploymentProgressing)
		if cond != nil && cond.Reason == utils.TimedOutReason {
			return "", false, fmt.Errorf("deployment %q exceeded its progress deadline", deployment.Name)
		}
		if deployment.Spec.Replicas != nil && deployment.Status.UpdatedReplicas < *deployment.Spec.Replicas {
			return fmt.Sprintf("Waiting for deployment %q rollout to finish: %d out of %d new replicas have been updated...\n", deployment.Name, deployment.Status.UpdatedReplicas, *deployment.Spec.Replicas), false, nil
		}
		if deployment.Status.Replicas > deployment.Status.UpdatedReplicas {
			return fmt.Sprintf("Waiting for deployment %q rollout to finish: %d old replicas are pending termination...\n", deployment.Name, deployment.Status.Replicas-deployment.Status.UpdatedReplicas), false, nil
		}
		if deployment.Status.AvailableReplicas < deployment.Status.UpdatedReplicas {
			return fmt.Sprintf("Waiting for deployment %q rollout to finish: %d of %d updated replicas are available...\n", deployment.Name, deployment.Status.AvailableReplicas, deployment.Status.UpdatedReplicas), false, nil
		}
		return fmt.Sprintf("deployment %q successfully rolled out\n", deployment.Name), true, nil
	}
	return fmt.Sprintf("Waiting for deployment spec update to be observed...\n"), false, nil
}
