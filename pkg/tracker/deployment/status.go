package deployment

import (
	"fmt"

	"github.com/flant/kubedog/pkg/tracker/indicators"
	"github.com/flant/kubedog/pkg/tracker/pod"
	"github.com/flant/kubedog/pkg/utils"

	appsv1 "k8s.io/api/apps/v1"
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

		res.IsReady = true
		if object.Status.UpdatedReplicas != *object.Spec.Replicas {
			res.IsReady = false
			res.WaitingForMessages = append(res.WaitingForMessages, fmt.Sprintf("up-to-date %d->%d", object.Status.UpdatedReplicas, *object.Spec.Replicas))
		}
		if object.Status.Replicas != *object.Spec.Replicas {
			res.IsReady = false
			res.WaitingForMessages = append(res.WaitingForMessages, fmt.Sprintf("replicas %d->%d", object.Status.Replicas, *object.Spec.Replicas))
		}
		if object.Status.AvailableReplicas != *object.Spec.Replicas {
			res.IsReady = false
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

// Status returns a message describing deployment status, and a bool value indicating if the status is considered done.
func DeploymentRolloutStatus(deployment *appsv1.Deployment, revision int64) (string, bool, error) {
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
		cond := utils.GetDeploymentCondition(deployment.Status, appsv1.DeploymentProgressing)
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
