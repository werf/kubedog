package statefulset

import (
	"fmt"

	"github.com/flant/kubedog/pkg/tracker/indicators"
	"github.com/flant/kubedog/pkg/tracker/pod"

	appsv1 "k8s.io/api/apps/v1"
)

type StatefulSetStatus struct {
	appsv1.StatefulSetStatus

	StatusGeneration uint64

	ReplicasIndicator *indicators.Int64GreaterOrEqualConditionIndicator
	ReadyIndicator    *indicators.Int64GreaterOrEqualConditionIndicator
	UpToDateIndicator *indicators.Int64GreaterOrEqualConditionIndicator

	IsReady      bool
	IsFailed     bool
	FailedReason string

	Pods         map[string]pod.PodStatus
	NewPodsNames []string
}

func NewStatefulSetStatus(object *appsv1.StatefulSet, statusGeneration uint64, isFailed bool, failedReason string, podsStatuses map[string]pod.PodStatus, newPodsNames []string) StatefulSetStatus {
	res := StatefulSetStatus{
		StatusGeneration:  statusGeneration,
		StatefulSetStatus: object.Status,
		Pods:              make(map[string]pod.PodStatus),
		NewPodsNames:      newPodsNames,
		IsReady:           true,
		IsFailed:          isFailed,
		FailedReason:      failedReason,
	}

	// TODO: share common code from deploy, ds and sts
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

	//if sts.Spec.UpdateStrategy.Type != appsv1.RollingUpdateStatefulSetStrategyType {
	//	return "", true, fmt.Errorf("rollout status is only available for %s strategy type", appsv1.RollingUpdateStatefulSetStrategyType)
	//}

	if object.Status.ObservedGeneration == 0 || object.Generation > object.Status.ObservedGeneration {
		// 		return "Waiting for statefulset spec update to be observed...\n", false, nil
		//fmt.Printf("Waiting for statefulset spec update to be observed...\n", object.Status)
		res.IsReady = false
	}

	if object.Spec.Replicas != nil {
		res.ReplicasIndicator = &indicators.Int64GreaterOrEqualConditionIndicator{
			Value:       int64(object.Status.Replicas),
			TargetValue: int64(*object.Spec.Replicas),
		}
		res.ReadyIndicator = &indicators.Int64GreaterOrEqualConditionIndicator{
			Value:       int64(object.Status.ReadyReplicas),
			TargetValue: int64(*object.Spec.Replicas),
		}

		if object.Status.ReadyReplicas < *object.Spec.Replicas {
			//return fmt.Sprintf("Waiting for %d pods to be ready...\n", *sts.Spec.Replicas-sts.Status.ReadyReplicas), false, nil
			fmt.Printf("Waiting for %d pods to be ready...\n", *object.Spec.Replicas-object.Status.ReadyReplicas)
			res.IsReady = false
		}
	} else {
		res.IsReady = false
	}

	switch object.Spec.UpdateStrategy.Type {
	case appsv1.RollingUpdateStatefulSetStrategyType:
		if object.Spec.Replicas != nil {
			if object.Spec.UpdateStrategy.RollingUpdate != nil && object.Spec.UpdateStrategy.RollingUpdate.Partition != nil {
				// Partitioned rollout

				res.UpToDateIndicator = &indicators.Int64GreaterOrEqualConditionIndicator{
					Value:       int64(object.Status.UpdatedReplicas),
					TargetValue: int64(*object.Spec.Replicas - *object.Spec.UpdateStrategy.RollingUpdate.Partition),
				}

				if object.Status.UpdatedReplicas < (*object.Spec.Replicas - *object.Spec.UpdateStrategy.RollingUpdate.Partition) {
					//return fmt.Sprintf("Waiting for partitioned roll out to finish: %d out of %d new pods have been updated...\n",
					//	sts.Status.UpdatedReplicas, *sts.Spec.Replicas-*sts.Spec.UpdateStrategy.RollingUpdate.Partition), false, nil
					//fmt.Printf("Waiting for partitioned roll out to finish: %d out of %d new pods have been updated...\n", object.Status.UpdatedReplicas, *object.Spec.Replicas-*object.Spec.UpdateStrategy.RollingUpdate.Partition)
					res.IsReady = false
				}
				//return fmt.Sprintf("partitioned roll out complete: %d new pods have been updated...\n",
				//	sts.Status.UpdatedReplicas), true, nil
			} else {
				// Not a partitioned rollout

				res.UpToDateIndicator = &indicators.Int64GreaterOrEqualConditionIndicator{
					Value:       int64(object.Status.UpdatedReplicas),
					TargetValue: int64(*object.Spec.Replicas),
				}

				if object.Status.UpdateRevision != object.Status.CurrentRevision {
					//return fmt.Sprintf("waiting for statefulset rolling update to complete %d pods at revision %s...\n",
					//	sts.Status.UpdatedReplicas, sts.Status.UpdateRevision), false, nil
					//fmt.Printf("waiting for statefulset rolling update to complete %d pods at revision %s...\n", object.Status.UpdatedReplicas, object.Status.UpdateRevision)
					res.IsReady = false
				}
				//return fmt.Sprintf("statefulset rolling update complete %d pods at revision %s...\n", sts.Status.CurrentReplicas, sts.Status.CurrentRevision), true, nil
			}
		} else {
			res.IsReady = false
		}

	case appsv1.OnDeleteStatefulSetStrategyType:
		if object.Spec.Replicas != nil {
			res.UpToDateIndicator = &indicators.Int64GreaterOrEqualConditionIndicator{
				Value:       int64(object.Status.UpdatedReplicas),
				TargetValue: int64(*object.Spec.Replicas),
			}

			if object.Status.UpdatedReplicas < *object.Spec.Replicas {
				res.IsReady = false
				fmt.Printf("User needs to delete old pods manually!\n")
			}
		} else {
			res.IsReady = false
		}

	default:
		panic(fmt.Sprintf("StatefulSet %s UpdateStrategy.Type %#v is not supported", object.Name, object.Spec.UpdateStrategy.Type))
	}

	if object.Spec.UpdateStrategy.Type == appsv1.RollingUpdateStatefulSetStrategyType && object.Spec.UpdateStrategy.RollingUpdate != nil {
	} else {
		res.UpToDateIndicator = &indicators.Int64GreaterOrEqualConditionIndicator{
			Value:       int64(object.Status.UpdatedReplicas),
			TargetValue: int64(*object.Spec.Replicas),
		}

		if object.Spec.UpdateStrategy.Type == appsv1.OnDeleteStatefulSetStrategyType {
			if !res.IsReady {
				//fmt.Printf("User needs to delete old pods manually!\n")
			}
		} else {
		}
	}

	return res
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
