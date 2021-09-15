package deployment

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/werf/kubedog/pkg/tracker/debug"
	"github.com/werf/kubedog/pkg/utils"
)

func getDeploymentStatus(client kubernetes.Interface, prevObj *appsv1.Deployment, newObj *appsv1.Deployment) string {
	if prevObj == nil {
		prevObj = newObj
	}
	msgs := []string{}

	for _, c := range newObj.Status.Conditions {
		msgs = append(msgs, fmt.Sprintf("        - %s - %s - %s: \"%s\"", c.Type, c.Status, c.Reason, c.Message))
	}
	msgs = append(msgs, fmt.Sprintf("        prg: %v, tim: %v,    gn: %d, ogn: %d, des: %d, rdy: %d, upd: %d, avl: %d, uav: %d",
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

func getReplicaSetsStatus(ctx context.Context, client kubernetes.Interface, deployment *appsv1.Deployment) string {
	msgs := []string{}

	_, allOlds, newRs, err := utils.GetAllReplicaSets(ctx, deployment, client)
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
