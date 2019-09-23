package utils

import (
	"fmt"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type ControllerMetadata interface {
	NewReplicaSetTemplate() corev1.PodTemplateSpec
	LabelSelector() *metav1.LabelSelector
	Namespace() string
	Name() string
	UID() types.UID
}

type ReplicaSetControllerWrapper struct {
	replicaSetTemplate corev1.PodTemplateSpec
	labelSelector      *metav1.LabelSelector
	metadata           metav1.Object
	deployment         *appsv1.Deployment
	statefulSet        *appsv1.StatefulSet
	daemonSet          *appsv1.DaemonSet
}

func (w *ReplicaSetControllerWrapper) NewReplicaSetTemplate() corev1.PodTemplateSpec {
	return w.replicaSetTemplate
}
func (w *ReplicaSetControllerWrapper) LabelSelector() *metav1.LabelSelector {
	return w.labelSelector
}
func (w *ReplicaSetControllerWrapper) Namespace() string {
	return w.metadata.GetNamespace()
}
func (w *ReplicaSetControllerWrapper) Name() string {
	return w.metadata.GetName()
}
func (w *ReplicaSetControllerWrapper) UID() types.UID {
	return w.metadata.GetUID()
}

func ControllerAccessor(controller interface{}) ControllerMetadata {
	w := &ReplicaSetControllerWrapper{}
	var err error
	w.metadata, err = meta.Accessor(controller)
	if err != nil {
		if debug() {
			fmt.Printf("ControllerAccessor for %T metadata error: %v", controller, err)
		}
	}

	switch c := controller.(type) {
	case *appsv1.Deployment:
		w.replicaSetTemplate = corev1.PodTemplateSpec{
			ObjectMeta: c.Spec.Template.ObjectMeta,
			Spec:       c.Spec.Template.Spec,
		}
		w.labelSelector = c.Spec.Selector
	case *appsv1.StatefulSet:
		w.replicaSetTemplate = corev1.PodTemplateSpec{
			ObjectMeta: c.Spec.Template.ObjectMeta,
			Spec:       c.Spec.Template.Spec,
		}
		w.labelSelector = c.Spec.Selector
	case *appsv1.DaemonSet:
		w.replicaSetTemplate = corev1.PodTemplateSpec{
			ObjectMeta: c.Spec.Template.ObjectMeta,
			Spec:       c.Spec.Template.Spec,
		}
		w.labelSelector = c.Spec.Selector
	}
	return w
}
