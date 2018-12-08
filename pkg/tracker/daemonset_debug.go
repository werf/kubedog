package tracker

import (
	"fmt"
	"strings"

	extensions "k8s.io/api/extensions/v1beta1"
)

func getDaemonSetStatus(obj *extensions.DaemonSet) string {
	msgs := []string{}

	for _, c := range obj.Status.Conditions {
		msgs = append(msgs, fmt.Sprintf("        - %s - %s - %s: \"%s\"", c.Type, c.Status, c.Reason, c.Message))
	}
	_, ready, _ := DaemonSetRolloutStatus(obj)
	msgs = append(msgs, fmt.Sprintf("        ready: %v,    gn: %d, ogn: %d,    ndsrd: %d, ncurr: %d, nupd: %d, nrdy: %d",
		yesNo(ready),
		obj.Generation,
		obj.Status.ObservedGeneration,
		obj.Status.DesiredNumberScheduled,
		obj.Status.CurrentNumberScheduled,
		obj.Status.UpdatedNumberScheduled,
		obj.Status.NumberReady,
	))
	return strings.Join(msgs, "\n")
}
