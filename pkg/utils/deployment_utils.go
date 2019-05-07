package utils

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/flant/kubedog/pkg/tracker"

	corev1 "k8s.io/api/core/v1"
	extensions "k8s.io/api/extensions/v1beta1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

const (
	// RevisionAnnotation is the revision annotation of a deployment's replica sets which records its rollout sequence
	RevisionAnnotation = "deployment.kubernetes.io/revision"
	// TimedOutReason is added in a deployment when its newest replica set fails to show any progress
	// within the given deadline (progressDeadlineSeconds).
	TimedOutReason = "ProgressDeadlineExceeded"
)

//func DeploymentCompleteAll(deployment *extensions.Deployment) {

//}

func equalSign(isEqual bool) string {
	if isEqual {
		return "=="
	} else {
		return "!="
	}
}

func greaterOrEqualSign(isGreaterOrEqual bool) string {
	if isGreaterOrEqual {
		return ">="
	} else {
		return "<"
	}
}

// DeploymentReadyStatus considers a deployment to be complete once all of its desired replicas
// are updated and available, and no old pods are running.
func DeploymentReadyStatus(deployment *extensions.Deployment, newStatus *extensions.DeploymentStatus) tracker.ReadyStatus {
	res := tracker.ReadyStatus{IsReady: true, IsProgressing: true}

	var isSatisfied bool

	isSatisfied = newStatus.Replicas == *(deployment.Spec.Replicas)
	res.ReadyConditions = append(res.ReadyConditions, tracker.ReadyCondition{
		Message:     fmt.Sprintf("overall %d %s %d", newStatus.Replicas, equalSign(isSatisfied), *(deployment.Spec.Replicas)),
		IsSatisfied: isSatisfied,
	})

	isSatisfied = newStatus.UpdatedReplicas == *(deployment.Spec.Replicas)
	res.ReadyConditions = append(res.ReadyConditions, tracker.ReadyCondition{
		Message:     fmt.Sprintf("updated %d/%d", newStatus.UpdatedReplicas, *(deployment.Spec.Replicas)),
		IsSatisfied: isSatisfied,
	})

	isSatisfied = newStatus.AvailableReplicas == *(deployment.Spec.Replicas)
	res.ReadyConditions = append(res.ReadyConditions, tracker.ReadyCondition{
		Message:     fmt.Sprintf("available %d/%d", newStatus.AvailableReplicas, *(deployment.Spec.Replicas)),
		IsSatisfied: isSatisfied,
	})

	isSatisfied = newStatus.ObservedGeneration >= deployment.Generation
	res.ReadyConditions = append(res.ReadyConditions, tracker.ReadyCondition{
		Message:     fmt.Sprintf("observed generation %d %s %d", deployment.Generation, greaterOrEqualSign(isSatisfied), newStatus.ObservedGeneration),
		IsSatisfied: isSatisfied,
	})

	for _, cond := range res.ReadyConditions {
		res.IsReady = (res.IsReady && cond.IsSatisfied)
	}

	oldStatus := deployment.Status
	// Old replicas that need to be scaled down
	oldStatusOldReplicas := oldStatus.Replicas - oldStatus.UpdatedReplicas
	newStatusOldReplicas := newStatus.Replicas - newStatus.UpdatedReplicas

	var msg string

	isSatisfied = newStatus.UpdatedReplicas > oldStatus.UpdatedReplicas
	if isSatisfied {
		msg = fmt.Sprintf("updated replicas increased %d => %d", oldStatus.UpdatedReplicas, newStatus.UpdatedReplicas)
	} else {
		msg = fmt.Sprintf("updated replicas not changed %d", oldStatus.UpdatedReplicas)
	}
	res.ProgressingConditions = append(res.ProgressingConditions, tracker.ProgressingCondition{
		Message:     msg,
		IsSatisfied: isSatisfied,
	})

	isSatisfied = newStatusOldReplicas < oldStatusOldReplicas
	if isSatisfied {
		msg = fmt.Sprintf("old replicas decreased %d => %d", oldStatusOldReplicas, newStatusOldReplicas)
	} else {
		msg = fmt.Sprintf("old replicas not changed %d", oldStatusOldReplicas)
	}
	res.ProgressingConditions = append(res.ProgressingConditions, tracker.ProgressingCondition{
		Message:     msg,
		IsSatisfied: isSatisfied,
	})

	isSatisfied = newStatus.AvailableReplicas > oldStatus.AvailableReplicas
	if isSatisfied {
		msg = fmt.Sprintf("available replicas increased %d => %d", oldStatus.AvailableReplicas, newStatus.AvailableReplicas)
	} else {
		msg = fmt.Sprintf("available replicas not changed %d", oldStatus.AvailableReplicas)
	}
	res.ProgressingConditions = append(res.ProgressingConditions, tracker.ProgressingCondition{
		Message:     msg,
		IsSatisfied: isSatisfied,
	})

	for _, cond := range res.ProgressingConditions {
		res.IsProgressing = (res.IsProgressing && cond.IsSatisfied)
	}

	return res
}

// DeploymentProgressing reports progress for a deployment. Progress is estimated by comparing the
// current with the new status of the deployment that the controller is observing. More specifically,
// when new pods are scaled up or become available, or old pods are scaled down, then we consider the
// deployment is progressing.
func DeploymentProgressing(deployment *extensions.Deployment, newStatus *extensions.DeploymentStatus) bool {
	oldStatus := deployment.Status

	// Old replicas that need to be scaled down
	oldStatusOldReplicas := oldStatus.Replicas - oldStatus.UpdatedReplicas
	newStatusOldReplicas := newStatus.Replicas - newStatus.UpdatedReplicas

	return (newStatus.UpdatedReplicas > oldStatus.UpdatedReplicas) ||
		(newStatusOldReplicas < oldStatusOldReplicas) ||
		newStatus.AvailableReplicas > deployment.Status.AvailableReplicas
}

var nowFn = func() time.Time { return time.Now() }

// DeploymentTimedOut considers a deployment to have timed out once its condition that reports progress
// is older than progressDeadlineSeconds or a Progressing condition with a TimedOutReason reason already
// exists.
func DeploymentTimedOut(deployment *extensions.Deployment, newStatus *extensions.DeploymentStatus) bool {
	if deployment.Spec.ProgressDeadlineSeconds == nil {
		return false
	}

	// Look for the Progressing condition. If it doesn't exist, we have no base to estimate progress.
	// If it's already set with a TimedOutReason reason, we have already timed out, no need to check
	// again.
	condition := GetDeploymentCondition(*newStatus, extensions.DeploymentProgressing)
	if condition == nil {
		return false
	}
	if condition.Reason == TimedOutReason {
		return true
	}

	// Look at the difference in seconds between now and the last time we reported any
	// progress or tried to create a replica set, or resumed a paused deployment and
	// compare against progressDeadlineSeconds.
	from := condition.LastUpdateTime
	now := nowFn()
	delta := time.Duration(*deployment.Spec.ProgressDeadlineSeconds) * time.Second
	timedOut := from.Add(delta).Before(now)

	if debug() {
		fmt.Printf("deploy/%s timedOut=%t [last progress check: %v - now: %v]\n", deployment.Name, timedOut, from.Format("02.01.2006 15:04:05"), now.Format("02.01.2006 15:04:05"))
	}
	return timedOut
}

// GetDeploymentCondition returns the condition with the provided type.
func GetDeploymentCondition(status extensions.DeploymentStatus, condType extensions.DeploymentConditionType) *extensions.DeploymentCondition {
	for i := range status.Conditions {
		c := status.Conditions[i]
		if c.Type == condType {
			return &c
		}
	}
	return nil
}

// Revision returns the revision number of the input object.
func Revision(obj runtime.Object) (int64, error) {
	acc, err := meta.Accessor(obj)
	if err != nil {
		return 0, err
	}
	v, ok := acc.GetAnnotations()[RevisionAnnotation]
	if !ok {
		return 0, nil
	}
	return strconv.ParseInt(v, 10, 64)
}

type rsListFunc func(string, metav1.ListOptions) ([]*extensions.ReplicaSet, error)

// rsListFromClient returns an rsListFunc that wraps the given client.
func rsListFromClient(c kubernetes.Interface) rsListFunc {
	return func(namespace string, options metav1.ListOptions) ([]*extensions.ReplicaSet, error) {
		rsList, err := c.ExtensionsV1beta1().ReplicaSets(namespace).List(options)
		if err != nil {
			return nil, err
		}
		var ret []*extensions.ReplicaSet
		for i := range rsList.Items {
			ret = append(ret, &rsList.Items[i])
		}
		return ret, err
	}
}

// GetAllReplicaSets returns the old and new replica sets targeted by the given Deployment. It gets PodList and ReplicaSetList from client interface.
// Note that the first set of old replica sets doesn't include the ones with no pods, and the second set of old replica sets include all old replica sets.
// The third returned value is the new replica set, and it may be nil if it doesn't exist yet.
func GetAllReplicaSets(deployment *extensions.Deployment, c kubernetes.Interface) ([]*extensions.ReplicaSet, []*extensions.ReplicaSet, *extensions.ReplicaSet, error) {
	rsList, err := ListReplicaSets(deployment, rsListFromClient(c))
	if err != nil {
		return nil, nil, nil, err
	}
	oldRSes, allOldRSes, err := FindOldReplicaSets(deployment, rsList)
	if err != nil {
		return nil, nil, nil, err
	}
	newRS, err := FindNewReplicaSet(deployment, rsList)
	if err != nil {
		return nil, nil, nil, err
	}
	return oldRSes, allOldRSes, newRS, nil
}

// FindNewReplicaSet returns the new RS this given deployment targets (the one with the same pod template).
func FindNewReplicaSet(deployment *extensions.Deployment, rsList []*extensions.ReplicaSet) (*extensions.ReplicaSet, error) {
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
	// new ReplicaSet does not exist.
	return nil, nil
}

func IsReplicaSetNew(deployment *extensions.Deployment, rsMap map[string]*extensions.ReplicaSet, rsName string) (bool, error) {
	rsList := []*extensions.ReplicaSet{}
	for _, rs := range rsMap {
		rsList = append(rsList, rs)
	}

	newRs, err := FindNewReplicaSet(deployment, rsList)
	if err != nil {
		return false, err
	}

	return (newRs != nil) && (rsName == newRs.Name), nil
}

// GetNewReplicaSetTemplate returns the desired PodTemplateSpec for the new ReplicaSet corresponding to the given ReplicaSet.
// Callers of this helper need to set the DefaultDeploymentUniqueLabelKey k/v pair.
func GetNewReplicaSetTemplate(deployment *extensions.Deployment) corev1.PodTemplateSpec {
	// newRS will have the same template as in deployment spec.
	return corev1.PodTemplateSpec{
		ObjectMeta: deployment.Spec.Template.ObjectMeta,
		Spec:       deployment.Spec.Template.Spec,
	}
}

// FindOldReplicaSets returns the old replica sets targeted by the given Deployment, with the given slice of RSes.
// Note that the first set of old replica sets doesn't include the ones with no pods, and the second set of old replica sets include all old replica sets.
func FindOldReplicaSets(deployment *extensions.Deployment, rsList []*extensions.ReplicaSet) ([]*extensions.ReplicaSet, []*extensions.ReplicaSet, error) {
	var requiredRSs []*extensions.ReplicaSet
	var allRSs []*extensions.ReplicaSet
	newRS, err := FindNewReplicaSet(deployment, rsList)
	if err != nil {
		return nil, nil, err
	}
	for _, rs := range rsList {
		// Filter out new replica set
		if newRS != nil && rs.UID == newRS.UID {
			continue
		}
		allRSs = append(allRSs, rs)
		if *(rs.Spec.Replicas) != 0 {
			requiredRSs = append(requiredRSs, rs)
		}
	}
	return requiredRSs, allRSs, nil
}

// ListReplicaSets returns a slice of RSes the given deployment targets.
// Note that this does NOT attempt to reconcile ControllerRef (adopt/orphan),
// because only the controller itself should do that.
// However, it does filter out anything whose ControllerRef doesn't match.
func ListReplicaSets(deployment *extensions.Deployment, getRSList rsListFunc) ([]*extensions.ReplicaSet, error) {
	// TODO: Right now we list replica sets by their labels. We should list them by selector, i.e. the replica set's selector
	//       should be a superset of the deployment's selector, see https://github.com/kubernetes/kubernetes/issues/19830.
	namespace := deployment.Namespace
	selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil {
		return nil, err
	}
	options := metav1.ListOptions{LabelSelector: selector.String()}
	all, err := getRSList(namespace, options)
	if err != nil {
		return all, err
	}
	// Only include those whose ControllerRef matches the Deployment.
	owned := make([]*extensions.ReplicaSet, 0, len(all))
	for _, rs := range all {
		controllerRef := GetControllerOf(rs)
		if controllerRef != nil && controllerRef.UID == deployment.UID {
			owned = append(owned, rs)
		}
	}
	return owned, nil
}

// EqualIgnoreHash returns true if two given podTemplateSpec are equal, ignoring the diff in value of Labels[pod-template-hash]
// We ignore pod-template-hash because the hash result would be different upon podTemplateSpec API changes
// (e.g. the addition of a new field will cause the hash code to change)
// Note that we assume input podTemplateSpecs contain non-empty labels
func EqualIgnoreHash(template1, template2 corev1.PodTemplateSpec) bool {
	// First, compare template.Labels (ignoring hash)
	labels1, labels2 := template1.Labels, template2.Labels
	if len(labels1) > len(labels2) {
		labels1, labels2 = labels2, labels1
	}
	// We make sure len(labels2) >= len(labels1)
	for k, v := range labels2 {
		if labels1[k] != v && k != extensions.DefaultDeploymentUniqueLabelKey {
			return false
		}
	}
	// Then, compare the templates without comparing their labels
	template1.Labels, template2.Labels = nil, nil
	return apiequality.Semantic.DeepEqual(template1, template2)
}

// ReplicaSetsByCreationTimestamp sorts a list of ReplicaSet by creation timestamp, using their names as a tie breaker.
type ReplicaSetsByCreationTimestamp []*extensions.ReplicaSet

func (o ReplicaSetsByCreationTimestamp) Len() int      { return len(o) }
func (o ReplicaSetsByCreationTimestamp) Swap(i, j int) { o[i], o[j] = o[j], o[i] }
func (o ReplicaSetsByCreationTimestamp) Less(i, j int) bool {
	if o[i].CreationTimestamp.Equal(&o[j].CreationTimestamp) {
		return o[i].Name < o[j].Name
	}
	return o[i].CreationTimestamp.Before(&o[j].CreationTimestamp)
}

// GetControllerOf returns the controllerRef if controllee has a controller,
// otherwise returns nil.
func GetControllerOf(controllee metav1.Object) *metav1.OwnerReference {
	ownerRefs := controllee.GetOwnerReferences()
	for i := range ownerRefs {
		owner := &ownerRefs[i]
		if owner.Controller != nil && *owner.Controller == true {
			return owner
		}
	}
	return nil
}

type PodListFunc func(string, metav1.ListOptions) (*corev1.PodList, error)

// PodListFromClient returns an PodListFunc that wraps the given client.
func PodListFromClient(c kubernetes.Interface) PodListFunc {
	return func(namespace string, options metav1.ListOptions) (*corev1.PodList, error) {
		podList, err := c.CoreV1().Pods(namespace).List(options)
		if err != nil {
			return nil, err
		}
		return podList, nil
	}
}

// ListPods returns a list of pods the given deployment targets.
// This needs a list of ReplicaSets for the Deployment,
// which can be found with ListReplicaSets().
// Note that this does NOT attempt to reconcile ControllerRef (adopt/orphan),
// because only the controller itself should do that.
// However, it does filter out anything whose ControllerRef doesn't match.
func ListPods(deployment *extensions.Deployment, rsList []*extensions.ReplicaSet, getPodList PodListFunc) (*corev1.PodList, error) {
	namespace := deployment.Namespace
	selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil {
		return nil, err
	}
	options := metav1.ListOptions{LabelSelector: selector.String()}
	all, err := getPodList(namespace, options)
	if err != nil {
		return all, err
	}
	// Only include those whose ControllerRef points to a ReplicaSet that is in
	// turn owned by this Deployment.
	rsMap := make(map[types.UID]bool, len(rsList))
	for _, rs := range rsList {
		rsMap[rs.UID] = true
	}
	owned := &corev1.PodList{Items: make([]corev1.Pod, 0, len(all.Items))}
	for i := range all.Items {
		pod := &all.Items[i]
		controllerRef := metav1.GetControllerOf(pod)
		if controllerRef != nil && rsMap[controllerRef.UID] {
			owned.Items = append(owned.Items, *pod)
		}
	}
	return owned, nil
}

func debug() bool {
	return os.Getenv("KUBEDOG_TRACKER_DEBUG") == "1"
}
