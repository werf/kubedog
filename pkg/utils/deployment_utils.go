package utils

import (
	"os"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
)

func FindNewReplicaSet(deployment *appsv1.Deployment, rsList []*appsv1.ReplicaSet) (*appsv1.ReplicaSet, error) {
	newRSTemplate := GetNewReplicaSetTemplate(deployment)
	sort.Sort(ReplicaSetsByCreationTimestamp(rsList))
	for i := range rsList {
		if EqualIgnoreHash(rsList[i].Spec.Template, newRSTemplate) {
			// In rare cases, such as after cluster upgrades, Deployment may end up with
			// having more than one new ReplicaSets that have the same template as its template,
			// see https://github.com/kubernetes/kubernetes/issues/40415
			// We deterministically choose the oldest new ReplicaSet.
			return rsList[i], nil
		}
	}
	return nil, nil
}

func IsReplicaSetNew(deployment *appsv1.Deployment, rsMap map[string]*appsv1.ReplicaSet, rsName string) (bool, error) {
	rsList := []*appsv1.ReplicaSet{}
	for _, rs := range rsMap {
		rsList = append(rsList, rs)
	}

	newRs, err := FindNewReplicaSet(deployment, rsList)
	if err != nil {
		return false, err
	}

	return (newRs != nil) && (rsName == newRs.Name), nil
}

func GetNewReplicaSetTemplate(deployment *appsv1.Deployment) corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		ObjectMeta: deployment.Spec.Template.ObjectMeta,
		Spec:       deployment.Spec.Template.Spec,
	}
}

func EqualIgnoreHash(template1, template2 corev1.PodTemplateSpec) bool {
	labels1, labels2 := template1.Labels, template2.Labels
	if len(labels1) > len(labels2) {
		labels1, labels2 = labels2, labels1
	}
	for k, v := range labels2 {
		if labels1[k] != v && k != appsv1.DefaultDeploymentUniqueLabelKey {
			return false
		}
	}
	template1.Labels, template2.Labels = nil, nil
	return apiequality.Semantic.DeepEqual(template1, template2)
}

type ReplicaSetsByCreationTimestamp []*appsv1.ReplicaSet

func (o ReplicaSetsByCreationTimestamp) Len() int      { return len(o) }
func (o ReplicaSetsByCreationTimestamp) Swap(i, j int) { o[i], o[j] = o[j], o[i] }
func (o ReplicaSetsByCreationTimestamp) Less(i, j int) bool {
	if o[i].CreationTimestamp.Equal(&o[j].CreationTimestamp) {
		return o[i].Name < o[j].Name
	}
	return o[i].CreationTimestamp.Before(&o[j].CreationTimestamp)
}

func debug() bool {
	return os.Getenv("KUBEDOG_TRACKER_DEBUG") == "1"
}
