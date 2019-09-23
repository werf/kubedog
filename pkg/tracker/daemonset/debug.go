package daemonset

import (
	"fmt"
	"strings"

	"github.com/flant/kubedog/pkg/tracker/debug"
	appsv1 "k8s.io/api/apps/v1"
)

func getDaemonSetStatus(obj *appsv1.DaemonSet) string {
	msgs := []string{}

	for _, c := range obj.Status.Conditions {
		msgs = append(msgs, fmt.Sprintf("        - %s - %s - %s: \"%s\"", c.Type, c.Status, c.Reason, c.Message))
	}
	_, ready, _ := DaemonSetRolloutStatus(obj)
	msgs = append(msgs, fmt.Sprintf("        ready: %v,    gn: %d, ogn: %d,    ndsrd: %d, ncurr: %d, nupd: %d, nrdy: %d",
		debug.YesNo(ready),
		obj.Generation,
		obj.Status.ObservedGeneration,
		obj.Status.DesiredNumberScheduled,
		obj.Status.CurrentNumberScheduled,
		obj.Status.UpdatedNumberScheduled,
		obj.Status.NumberReady,
	))
	return strings.Join(msgs, "\n")
}
