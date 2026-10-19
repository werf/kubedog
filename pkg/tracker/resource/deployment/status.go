package deployment

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"

	"github.com/werf/kubedog/pkg/tracker/pod"
)

// Status is a snapshot of the Deployment rollout. It only describes the state of the
// Deployment and has no presentation details: displaying it is up to the consumer.
type Status struct {
	Generation uint64

	// Object is the last observed Deployment, nil if it has not been observed yet or was deleted.
	Object *appsv1.Deployment

	Ready        bool
	Failed       bool
	FailedReason string
	WaitingFor   []string

	Pods map[string]pod.PodStatus
	// NewPodsNames are the Pods of the new ReplicaSet of the Deployment,
	// i.e. the actual up-to-date Pods of the Deployment.
	NewPodsNames []string
}

type ReplicaSet struct {
	Name  string
	IsNew bool
}

// Evaluate checks if the Deployment rollout is complete and, if it is not, what it waits for.
func Evaluate(object *appsv1.Deployment) (ready bool, waitingFor []string) {
	if object.Status.ObservedGeneration < object.Generation {
		return false, []string{fmt.Sprintf("observed generation %d should be >= %d", object.Status.ObservedGeneration, object.Generation)}
	}

	if object.Spec.Replicas == nil {
		return false, nil
	}

	replicas := *object.Spec.Replicas
	if object.Status.UpdatedReplicas != replicas {
		waitingFor = append(waitingFor, fmt.Sprintf("up-to-date %d->%d", object.Status.UpdatedReplicas, replicas))
	}
	if object.Status.Replicas != replicas {
		waitingFor = append(waitingFor, fmt.Sprintf("replicas %d->%d", object.Status.Replicas, replicas))
	}
	if object.Status.AvailableReplicas != replicas {
		waitingFor = append(waitingFor, fmt.Sprintf("available %d->%d", object.Status.AvailableReplicas, replicas))
	}

	return len(waitingFor) == 0, waitingFor
}
