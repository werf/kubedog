package statefulset

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"

	"github.com/werf/kubedog/pkg/tracker/indicators"
	"github.com/werf/kubedog/pkg/tracker/pod"
)

type StatefulSetStatus struct {
	appsv1.StatefulSetStatus

	StatusGeneration uint64

	ReplicasIndicator *indicators.Int64GreaterOrEqualConditionIndicator
	ReadyIndicator    *indicators.Int64GreaterOrEqualConditionIndicator
	UpToDateIndicator *indicators.Int64GreaterOrEqualConditionIndicator

	WaitingForMessages []string
	WarningMessages    []string

	IsReady      bool
	IsFailed     bool
	FailedReason string

	Pods         map[string]pod.PodStatus
	NewPodsNames []string
}

func NewStatefulSetStatus(object *appsv1.StatefulSet, statusGeneration uint64, isFailed bool, failedReason string, warningMessages []string, podsStatuses map[string]pod.PodStatus, newPodsNames []string) StatefulSetStatus {
	res := StatefulSetStatus{
		StatusGeneration:  statusGeneration,
		StatefulSetStatus: object.Status,
		Pods:              make(map[string]pod.PodStatus),
		NewPodsNames:      newPodsNames,
		IsReady:           true,
		IsFailed:          isFailed,
		FailedReason:      failedReason,
		WarningMessages:   warningMessages,
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

	if object.Status.ObservedGeneration == 0 || object.Generation > object.Status.ObservedGeneration {
		res.IsReady = false
		res.WaitingForMessages = append(res.WaitingForMessages, fmt.Sprintf("observed generation %d should be >= %d", object.Status.ObservedGeneration, object.Generation))
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
			res.IsReady = false
			res.WaitingForMessages = append(res.WaitingForMessages, fmt.Sprintf("ready %d->%d", object.Status.ReadyReplicas, *object.Spec.Replicas))
		}
	} else {
		res.IsReady = false
		res.WaitingForMessages = append(res.WaitingForMessages, "spec replicas should be set")
	}

	switch object.Spec.UpdateStrategy.Type {
	case appsv1.RollingUpdateStatefulSetStrategyType:
		if object.Spec.Replicas != nil {
			if object.Spec.UpdateStrategy.RollingUpdate != nil && object.Spec.UpdateStrategy.RollingUpdate.Partition != nil && *object.Spec.UpdateStrategy.RollingUpdate.Partition > 0 {
				// Partitioned rollout

				res.UpToDateIndicator = &indicators.Int64GreaterOrEqualConditionIndicator{
					Value:       int64(object.Status.UpdatedReplicas),
					TargetValue: int64(*object.Spec.Replicas - *object.Spec.UpdateStrategy.RollingUpdate.Partition),
				}

				if object.Status.UpdatedReplicas < (*object.Spec.Replicas - *object.Spec.UpdateStrategy.RollingUpdate.Partition) {
					res.IsReady = false
					res.WaitingForMessages = append(res.WaitingForMessages, fmt.Sprintf("up-to-date %d->%d (partitioned roll out)", object.Status.UpdatedReplicas, *object.Spec.Replicas-*object.Spec.UpdateStrategy.RollingUpdate.Partition))
				}
			} else {
				// Not a partitioned rollout

				res.UpToDateIndicator = &indicators.Int64GreaterOrEqualConditionIndicator{
					Value:       int64(object.Status.UpdatedReplicas),
					TargetValue: int64(*object.Spec.Replicas),
				}

				if object.Status.UpdateRevision != object.Status.CurrentRevision {
					res.IsReady = false
					res.WaitingForMessages = append(res.WaitingForMessages, fmt.Sprintf("up-to-date %d->%d", object.Status.UpdatedReplicas, *object.Spec.Replicas))
				}
			}
		}

	case appsv1.OnDeleteStatefulSetStrategyType:
		if object.Spec.Replicas != nil {
			res.UpToDateIndicator = &indicators.Int64GreaterOrEqualConditionIndicator{
				Value:       int64(object.Status.UpdatedReplicas),
				TargetValue: int64(*object.Spec.Replicas),
			}

			// Workaround for old kubernetes (1.10), which does not update UpdateReplicas field
			isReady := (object.Status.UpdatedReplicas == 0) && (object.Status.UpdateRevision == object.Status.CurrentRevision)

			if !isReady {
				isReady = object.Status.UpdatedReplicas >= *object.Spec.Replicas
			}

			if !isReady {
				res.IsReady = false
				res.WaitingForMessages = append(res.WaitingForMessages, fmt.Sprintf("up-to-date %d->%d (user should delete old pods manually now!)", object.Status.UpdatedReplicas, *object.Spec.Replicas))
			}
		}

	default:
		panic(fmt.Sprintf("StatefulSet %s UpdateStrategy.Type %#v is not supported", object.Name, object.Spec.UpdateStrategy.Type))
	}

	return res
}
