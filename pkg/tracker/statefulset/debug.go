package statefulset

import (
	"fmt"
	"strings"

	"github.com/flant/kubedog/pkg/tracker/debug"
	appsv1 "k8s.io/api/apps/v1"
)

func getStatefulSetStatus(obj *appsv1.StatefulSet) string {
	msgs := []string{}

	_, ready, _ := StatefulSetRolloutStatus(obj)
	complete := StatefulSetComplete(obj)
	msgs = append(msgs, fmt.Sprintf("        Ready: %v. Complete: %v. Generation: %d, observed gen: %d.",
		debug.YesNo(ready),
		debug.YesNo(complete),
		obj.Generation,
		obj.Status.ObservedGeneration))
	msgs = append(msgs, fmt.Sprintf("        Revision: current: %s, update: %s",
		obj.Status.CurrentRevision,
		obj.Status.UpdateRevision))
	rollingUpdateInfo := ""
	if obj.Spec.UpdateStrategy.Type == appsv1.RollingUpdateStatefulSetStrategyType {
		rollingUpdateInfo = ", rollingUpdate: nil"
		if obj.Spec.UpdateStrategy.RollingUpdate != nil {
			rollingUpdateInfo = ", rollingUpdate.Partition: nil"
			if obj.Spec.UpdateStrategy.RollingUpdate.Partition != nil {
				rollingUpdateInfo = fmt.Sprintf(", rollingUpdate.Partition: %d", *obj.Spec.UpdateStrategy.RollingUpdate.Partition)
			}
		}
	}
	msgs = append(msgs, fmt.Sprintf("        UpdateStrategy: %s%s", obj.Spec.UpdateStrategy.Type, rollingUpdateInfo))
	msgs = append(msgs, fmt.Sprintf("        Replicas: desired: %d, status: %d, current: %d, updated: %d, ready: %d",
		*obj.Spec.Replicas,
		obj.Status.Replicas,
		obj.Status.CurrentReplicas,
		obj.Status.UpdatedReplicas,
		obj.Status.ReadyReplicas,
	))

	cond := ""
	if len(obj.Status.Conditions) == 0 {
		cond = " empty"
	}
	msgs = append(msgs, fmt.Sprintf("        Conditions:%s", cond))
	for _, c := range obj.Status.Conditions {
		msgs = append(msgs, fmt.Sprintf("        - %s - %s - %s: \"%s\"", c.Type, c.Status, c.Reason, c.Message))
	}

	return strings.Join(msgs, "\n")
}
