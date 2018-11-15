package utils

import corev1 "k8s.io/api/core/v1"

func GetPodReplicaSetName(pod *corev1.Pod) string {
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "ReplicaSet" {
			return ref.Name
		}
	}
	return ""
}
