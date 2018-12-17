package tracker

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
)

func getStatefulSetStatus(obj *appsv1.StatefulSet) string {
	msgs := []string{}

	msgs = append(msgs, fmt.Sprintf("%#v\n", obj.Status))

	for _, c := range obj.Status.Conditions {
		msgs = append(msgs, fmt.Sprintf("        - %s - %s - %s: \"%s\"", c.Type, c.Status, c.Reason, c.Message))
	}
	_, ready, _ := StatefulSetRolloutStatus(obj)
	msgs = append(msgs, fmt.Sprintf("        ready: %v,    gn: %d, ogn: %d,     des: %d, cur: %d, upd: %d, rdy: %d",
		yesNo(ready),
		obj.Generation,
		obj.Status.ObservedGeneration,
		obj.Status.Replicas,
		obj.Status.CurrentReplicas,
		obj.Status.UpdatedReplicas,
		obj.Status.ReadyReplicas,
	))
	return strings.Join(msgs, "\n")
}
