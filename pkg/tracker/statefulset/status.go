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

	Pods map[string]pod.PodStatus
}

func NewStatefulSetStatus(object *appsv1.StatefulSet, statusGeneration uint64, isFailed bool, failedReason string, podsStatuses map[string]pod.PodStatus) StatefulSetStatus {
	res := StatefulSetStatus{
		StatusGeneration:  statusGeneration,
		StatefulSetStatus: object.Status,
		Pods:              make(map[string]pod.PodStatus),
		IsReady:           true,
		IsFailed:          isFailed,
		FailedReason:      failedReason,
	}

	for k, v := range podsStatuses {
		res.Pods[k] = v
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
