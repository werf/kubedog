package deployment

import (
	"fmt"
	"strings"

	extensions "k8s.io/api/extensions/v1beta1"

	"k8s.io/client-go/kubernetes"

	"github.com/flant/kubedog/pkg/tracker/debug"
	"github.com/flant/kubedog/pkg/utils"
)

func getDeploymentStatus(client kubernetes.Interface, prevObj *extensions.Deployment, newObj *extensions.Deployment) string {
	if prevObj == nil {
		prevObj = newObj
	}
	msgs := []string{}

	for _, c := range newObj.Status.Conditions {
		msgs = append(msgs, fmt.Sprintf("        - %s - %s - %s: \"%s\"", c.Type, c.Status, c.Reason, c.Message))
	}
	msgs = append(msgs, fmt.Sprintf("        cpl: %v, prg: %v, tim: %v,    gn: %d, ogn: %d, des: %d, rdy: %d, upd: %d, avl: %d, uav: %d",
		debug.YesNo(NewDeploymentReadyIndicator(prevObj, &newObj.Status).IsReady),
		debug.YesNo(utils.DeploymentProgressing(prevObj, &newObj.Status)),
		debug.YesNo(utils.DeploymentTimedOut(prevObj, &newObj.Status)),
		newObj.Generation,
		newObj.Status.ObservedGeneration,
		newObj.Status.Replicas,
		newObj.Status.ReadyReplicas,
		newObj.Status.UpdatedReplicas,
		newObj.Status.AvailableReplicas,
		newObj.Status.UnavailableReplicas,
	))
	return strings.Join(msgs, "\n")
}

func getReplicaSetsStatus(client kubernetes.Interface, deployment *extensions.Deployment) string {
	msgs := []string{}

	_, allOlds, newRs, err := utils.GetAllReplicaSets(deployment, client)
	if err != nil {
		msgs = append(msgs, fmt.Sprintf("waitForNewReplicaSet error: %v", err))
	} else {
		for i, rs := range allOlds {
			msgs = append(msgs, fmt.Sprintf(
				"        - old %2d: rs/%s gn: %d, ogn: %d, des: %d, rdy: %d, fll: %d, avl: %d",
				i, rs.Name,
				rs.Generation,
				rs.Status.ObservedGeneration,
				rs.Status.Replicas,
				rs.Status.ReadyReplicas,
				rs.Status.FullyLabeledReplicas,
				rs.Status.AvailableReplicas,
			))
		}
		if newRs != nil {
			msgs = append(msgs, fmt.Sprintf(
				"        - new   : rs/%s gn: %d, ogn: %d, des: %d, rdy: %d, fll: %d, avl: %d",
				newRs.Name,
				newRs.Generation,
				newRs.Status.ObservedGeneration,
				newRs.Status.Replicas,
				newRs.Status.ReadyReplicas,
				newRs.Status.FullyLabeledReplicas,
				newRs.Status.AvailableReplicas,
			))
		}
	}

	return strings.Join(msgs, "\n")
}
