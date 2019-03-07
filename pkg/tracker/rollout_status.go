/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

/*
k8s.io/kubernetes/pkg/kubectl/rollout_status.go
*/

package tracker

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	extensions "k8s.io/api/extensions/v1beta1"

	"github.com/flant/kubedog/pkg/utils"
)

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

// Status returns a message describing daemon set status, and a bool value indicating if the status is considered done.
func DaemonSetRolloutStatus(daemon *extensions.DaemonSet) (string, bool, error) {
	if daemon.Spec.UpdateStrategy.Type != extensions.RollingUpdateDaemonSetStrategyType {
		return "", true, fmt.Errorf("rollout status is only available for %s strategy type", extensions.RollingUpdateDaemonSetStrategyType)
	}
	if daemon.Generation <= daemon.Status.ObservedGeneration {
		if daemon.Status.UpdatedNumberScheduled < daemon.Status.DesiredNumberScheduled {
			return fmt.Sprintf("Waiting for daemon set %q rollout to finish: %d out of %d new pods have been updated...\n", daemon.Name, daemon.Status.UpdatedNumberScheduled, daemon.Status.DesiredNumberScheduled), false, nil
		}
		if daemon.Status.NumberAvailable < daemon.Status.DesiredNumberScheduled {
			return fmt.Sprintf("Waiting for daemon set %q rollout to finish: %d of %d updated pods are available...\n", daemon.Name, daemon.Status.NumberAvailable, daemon.Status.DesiredNumberScheduled), false, nil
		}
		return fmt.Sprintf("daemon set %q successfully rolled out\n", daemon.Name), true, nil
	}
	return fmt.Sprintf("Waiting for daemon set spec update to be observed...\n"), false, nil
}

// Status returns a message describing statefulset status, and a bool value indicating if the status is considered done.
// A code from kubectl sources. Doesn't work well for OnDelete, downscale and partition: 0 case.
// https://github.com/kubernetes/kubernetes/issues/72212
// Now used only for debug purposes
func StatefulSetRolloutStatus(sts *appsv1.StatefulSet) (string, bool, error) {
	if sts.Spec.UpdateStrategy.Type != appsv1.RollingUpdateStatefulSetStrategyType {
		return "", true, fmt.Errorf("rollout status is only available for %s strategy type", appsv1.RollingUpdateStatefulSetStrategyType)
	}
	if sts.Status.ObservedGeneration == 0 || sts.Generation > sts.Status.ObservedGeneration {
		return "Waiting for statefulset spec update to be observed...\n", false, nil
	}
	if sts.Spec.Replicas != nil && sts.Status.ReadyReplicas < *sts.Spec.Replicas {
		return fmt.Sprintf("Waiting for %d pods to be ready...\n", *sts.Spec.Replicas-sts.Status.ReadyReplicas), false, nil
	}
	if sts.Spec.UpdateStrategy.Type == appsv1.RollingUpdateStatefulSetStrategyType && sts.Spec.UpdateStrategy.RollingUpdate != nil {
		if sts.Spec.Replicas != nil && sts.Spec.UpdateStrategy.RollingUpdate.Partition != nil {
			if sts.Status.UpdatedReplicas < (*sts.Spec.Replicas - *sts.Spec.UpdateStrategy.RollingUpdate.Partition) {
				return fmt.Sprintf("Waiting for partitioned roll out to finish: %d out of %d new pods have been updated...\n",
					sts.Status.UpdatedReplicas, *sts.Spec.Replicas-*sts.Spec.UpdateStrategy.RollingUpdate.Partition), false, nil
			}
		}
		return fmt.Sprintf("partitioned roll out complete: %d new pods have been updated...\n",
			sts.Status.UpdatedReplicas), true, nil
	}
	if sts.Status.UpdateRevision != sts.Status.CurrentRevision {
		return fmt.Sprintf("waiting for statefulset rolling update to complete %d pods at revision %s...\n",
			sts.Status.UpdatedReplicas, sts.Status.UpdateRevision), false, nil
	}
	return fmt.Sprintf("statefulset rolling update complete %d pods at revision %s...\n", sts.Status.CurrentReplicas, sts.Status.CurrentRevision), true, nil

}

// StatefulSetComplete return true if StatefulSet is considered ready
//
// Two strategies: OnDelete, RollingUpdate
//
// OnDelete can be tracked in two situations:
// - resource is created
// - replicas attribute is changed
// A more sophisticated solution that will check Revision of Pods is not needed because of required manual intervention
//
// RollingUpdate is automatic, so we can rely on the CurrentReplicas and UpdatedReplicas counters.
func StatefulSetComplete(sts *appsv1.StatefulSet) bool {
	if sts.Status.ObservedGeneration == 0 || sts.Generation != sts.Status.ObservedGeneration {
		return false
	}

	// desired == observed == ready
	if sts.Spec.Replicas != nil && (*sts.Spec.Replicas != sts.Status.Replicas || *sts.Spec.Replicas != sts.Status.ReadyReplicas) {
		return false
	}

	// No other conditions for OnDelete strategy
	if sts.Spec.UpdateStrategy.Type == appsv1.OnDeleteStatefulSetStrategyType {
		return true
	}

	if sts.Spec.UpdateStrategy.Type == appsv1.RollingUpdateStatefulSetStrategyType {
		var partition int32 = 0
		if sts.Spec.UpdateStrategy.RollingUpdate != nil {
			if sts.Spec.Replicas != nil && sts.Spec.UpdateStrategy.RollingUpdate.Partition != nil {
				partition = *sts.Spec.UpdateStrategy.RollingUpdate.Partition
			}
		}

		if partition == 0 {
			// The last step in update is make revisions equal and so UpdatedReplicas becomes 0.
			// Final ready condition is: currentRevision == updateRevision and currentReplicas == readyReplicas and updatedReplicas == 0
			// This code also works for static checking when sts is not in progress.

			// Revision are not equal — sts update still in progress.
			if sts.Status.UpdateRevision != sts.Status.CurrentRevision {
				return false
			}
			//    current == ready, updated == 0
			// or current == ready, updated == current (1.10 set updatedReplicas to 0, but 1.11 is not)
			if sts.Status.CurrentReplicas == sts.Status.ReadyReplicas && (sts.Status.UpdatedReplicas == 0 || sts.Status.UpdatedReplicas == sts.Status.CurrentReplicas) {
				return true
			}
		} else {
			// Final ready condition for partitioned rollout is:
			// revisions are not equal, currentReplicas == partition, updatedReplicas == desired - partition
			if sts.Status.UpdateRevision == sts.Status.CurrentRevision {
				return false
			}
			if sts.Status.CurrentReplicas == partition && sts.Status.UpdatedReplicas == (*sts.Spec.Replicas-partition) {
				return true
			}
		}
		return false
	}

	// Unknown UpdateStrategy. Behave like OnDelete.
	return true
}
