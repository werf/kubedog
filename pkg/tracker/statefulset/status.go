package statefulset

import (
	"github.com/flant/kubedog/pkg/tracker/indicators"
	"github.com/flant/kubedog/pkg/tracker/pod"

	appsv1 "k8s.io/api/apps/v1"
)

type StatefulSetStatus struct {
	appsv1.StatefulSetStatus

	StatusGeneration uint64

	ReadyReplicasIndicator   *indicators.Int32EqualConditionIndicator
	CurrentReplicasIndicator *indicators.Int32EqualConditionIndicator
	UpdatedReplicasIndicator *indicators.Int32MultipleEqualConditialIndicator

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
		IsFailed:          isFailed,
		FailedReason:      failedReason,
	}

	for k, v := range podsStatuses {
		res.Pods[k] = v
	}

	if object.Status.ObservedGeneration == 0 || object.Generation != object.Status.ObservedGeneration {
		res.IsReady = false
		return res
	}

	if object.Spec.Replicas != nil {
		res.ReadyReplicasIndicator = &indicators.Int32EqualConditionIndicator{}
		res.ReadyReplicasIndicator.Value = object.Status.ReadyReplicas
		res.ReadyReplicasIndicator.TargetValue = *object.Spec.Replicas

		// desired == observed == ready
		if (*object.Spec.Replicas != object.Status.Replicas) || (*object.Spec.Replicas != object.Status.ReadyReplicas) {
			res.IsReady = false
			return res
		}
	}

	// No other conditions for OnDelete strategy
	// FIXME: OnDelete strategy tracker should wait till user manually deletes old replicas
	if object.Spec.UpdateStrategy.Type == appsv1.OnDeleteStatefulSetStrategyType {
		res.IsReady = true
		return res
	}

	if object.Spec.UpdateStrategy.Type == appsv1.RollingUpdateStatefulSetStrategyType {
		var partition int32 = 0
		if object.Spec.UpdateStrategy.RollingUpdate != nil {
			if object.Spec.Replicas != nil && object.Spec.UpdateStrategy.RollingUpdate.Partition != nil {
				partition = *object.Spec.UpdateStrategy.RollingUpdate.Partition
			}
		}

		if partition == 0 {
			// The last step in update is make revisions equal and so UpdatedReplicas becomes 0.
			// Final ready condition is: currentRevision == updateRevision and currentReplicas == readyReplicas and updatedReplicas == 0
			// This code also works for static checking when sts is not in progress.

			// Revision are not equal — sts update still in progress.
			if object.Status.UpdateRevision != object.Status.CurrentRevision {
				res.IsReady = false
				return res
			}

			res.CurrentReplicasIndicator = &indicators.Int32EqualConditionIndicator{
				Value:       object.Status.CurrentReplicas,
				TargetValue: object.Status.ReadyReplicas,
			}
			res.UpdatedReplicasIndicator = &indicators.Int32MultipleEqualConditialIndicator{
				Value:        object.Status.UpdatedReplicas,
				TargetValues: []int32{0, object.Status.CurrentReplicas},
			}

			//    current == ready, updated == 0
			// or current == ready, updated == current (1.10 set updatedReplicas to 0, but 1.11 is not)
			if object.Status.CurrentReplicas == object.Status.ReadyReplicas && (object.Status.UpdatedReplicas == 0 || object.Status.UpdatedReplicas == object.Status.CurrentReplicas) {
				res.IsReady = true
				return res
			}
		} else if object.Status.UpdateRevision == object.Status.CurrentRevision {
			// Final ready condition for partitioned rollout is:
			// revisions are not equal, currentReplicas == partition, updatedReplicas == desired - partition

			res.IsReady = false
			return res
		} else {
			res.CurrentReplicasIndicator = &indicators.Int32EqualConditionIndicator{
				Value:       object.Status.CurrentReplicas,
				TargetValue: partition,
			}
			res.UpdatedReplicasIndicator = &indicators.Int32MultipleEqualConditialIndicator{
				Value:        object.Status.UpdatedReplicas,
				TargetValues: []int32{(*object.Spec.Replicas - partition)},
			}

			if object.Status.CurrentReplicas == partition && object.Status.UpdatedReplicas == (*object.Spec.Replicas-partition) {
				res.IsReady = true
				return res
			}
		}

		res.IsReady = false
		return res
	}

	// Unknown UpdateStrategy. Behave like OnDelete.
	res.IsReady = true
	return res
}
