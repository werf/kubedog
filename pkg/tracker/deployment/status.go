package deployment

import (
	"fmt"

	"github.com/flant/kubedog/pkg/tracker/indicators"
	"github.com/flant/kubedog/pkg/tracker/pod"
	"github.com/flant/kubedog/pkg/utils"

	extensions "k8s.io/api/extensions/v1beta1"
)

type DeploymentStatus struct {
	extensions.DeploymentStatus

	StatusGeneration uint64

	OverallReplicasIndicator    *indicators.Int32EqualConditionIndicator
	UpdatedReplicasIndicator    *indicators.Int32EqualConditionIndicator
	AvailableReplicasIndicator  *indicators.Int32EqualConditionIndicator
	OldReplicasIndicator        *indicators.Int32EqualConditionIndicator
	ObservedGenerationIndicator *indicators.Int64GreaterOrEqualConditionIndicator

	IsReady      bool
	IsFailed     bool
	FailedReason string

	Pods map[string]pod.PodStatus
}

func NewDeploymentStatus(object *extensions.Deployment, statusGeneration uint64, isFailed bool, failedReason string, podsStatuses map[string]pod.PodStatus) DeploymentStatus {
	res := DeploymentStatus{
		StatusGeneration:            statusGeneration,
		DeploymentStatus:            object.Status,
		Pods:                        make(map[string]pod.PodStatus),
		OverallReplicasIndicator:    &indicators.Int32EqualConditionIndicator{},
		UpdatedReplicasIndicator:    &indicators.Int32EqualConditionIndicator{},
		AvailableReplicasIndicator:  &indicators.Int32EqualConditionIndicator{},
		OldReplicasIndicator:        &indicators.Int32EqualConditionIndicator{},
		ObservedGenerationIndicator: &indicators.Int64GreaterOrEqualConditionIndicator{},
		IsReady:                     true,
		IsFailed:                    isFailed,
		FailedReason:                failedReason,
	}

	for k, v := range podsStatuses {
		res.Pods[k] = v
	}

	res.OverallReplicasIndicator.Value = object.Status.Replicas
	res.OverallReplicasIndicator.TargetValue = *(object.Spec.Replicas)
	res.IsReady = res.IsReady && res.OverallReplicasIndicator.IsReady()

	res.UpdatedReplicasIndicator.Value = object.Status.UpdatedReplicas
	res.UpdatedReplicasIndicator.TargetValue = *(object.Spec.Replicas)
	res.IsReady = res.IsReady && res.UpdatedReplicasIndicator.IsReady()

	res.AvailableReplicasIndicator.Value = object.Status.AvailableReplicas
	res.AvailableReplicasIndicator.TargetValue = *(object.Spec.Replicas)
	res.IsReady = res.IsReady && res.AvailableReplicasIndicator.IsReady()

	res.OldReplicasIndicator.Value = object.Status.Replicas - object.Status.UpdatedReplicas
	res.OldReplicasIndicator.TargetValue = 0
	res.IsReady = res.IsReady && res.OldReplicasIndicator.IsReady()

	res.ObservedGenerationIndicator.Value = object.Status.ObservedGeneration
	res.ObservedGenerationIndicator.TargetValue = object.Generation
	res.IsReady = res.IsReady && res.ObservedGenerationIndicator.IsReady()

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
