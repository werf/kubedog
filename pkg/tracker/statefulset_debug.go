package tracker

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/client-go/kubernetes"
)

func getStatefulSetStatus(client kubernetes.Interface, prevObj *appsv1.StatefulSet, newObj *appsv1.StatefulSet) string {
	if prevObj == nil {
		prevObj = newObj
	}
	msgs := []string{}

	for _, c := range newObj.Status.Conditions {
		msgs = append(msgs, fmt.Sprintf("        - %s - %s - %s: \"%s\"", c.Type, c.Status, c.Reason, c.Message))
	}
	_, ready, _ := StatefulSetRolloutStatus(newObj)
	msgs = append(msgs, fmt.Sprintf("        cpl: %v, prg: %v, tim: %v,    gn: %d, ogn: %d, des: %d, rdy: %d, upd: %d, avl: %d",
		yesNo(ready),
		"-",
		"-",
		newObj.Generation,
		newObj.Status.ObservedGeneration,
		newObj.Status.Replicas,
		newObj.Status.CurrentReplicas,
		newObj.Status.UpdatedReplicas,
		newObj.Status.ReadyReplicas,
	))
	return strings.Join(msgs, "\n")
}
