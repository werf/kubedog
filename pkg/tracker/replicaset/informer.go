package replicaset

import (
	"context"
	"fmt"

	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	apilabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"

	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/tracker/pod"
	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
	"github.com/werf/kubedog/pkg/utils"
)

type ReplicaSet struct {
	Name  string
	IsNew bool
}

// TODO: add containers!
type ReplicaSetPod struct {
	ReplicaSet ReplicaSet
	Name       string
}

type ReplicaSetPodLogChunk struct {
	*pod.PodLogChunk
	ReplicaSet ReplicaSet
}

type ReplicaSetPodError struct {
	pod.PodError
	ReplicaSet ReplicaSet
}

// ReplicaSetInformer monitor ReplicaSet events to use with controllers (Deployment, StatefulSet, DaemonSet)
type ReplicaSetInformer struct {
	tracker.Tracker
	Controller         utils.ControllerMetadata
	ReplicaSetAdded    chan *appsv1.ReplicaSet
	ReplicaSetModified chan *appsv1.ReplicaSet
	ReplicaSetDeleted  chan *appsv1.ReplicaSet
	Errors             chan error
}

func NewReplicaSetInformer(trk *tracker.Tracker, controller utils.ControllerMetadata) *ReplicaSetInformer {
	return &ReplicaSetInformer{
		Tracker: tracker.Tracker{
			Kube:                            trk.Kube,
			Namespace:                       trk.Namespace,
			ResourceName:                    trk.ResourceName,
			FullResourceName:                trk.FullResourceName,
			InformerFactory:                 trk.InformerFactory,
			SaveLogsOnlyForNumberOfReplicas: trk.SaveLogsOnlyForNumberOfReplicas,
		},
		Controller:         controller,
		ReplicaSetAdded:    make(chan *appsv1.ReplicaSet, 1),
		ReplicaSetModified: make(chan *appsv1.ReplicaSet, 1),
		ReplicaSetDeleted:  make(chan *appsv1.ReplicaSet, 1),
		Errors:             make(chan error, 1),
	}
}

func (r *ReplicaSetInformer) WithChannels(added chan *appsv1.ReplicaSet,
	modified chan *appsv1.ReplicaSet,
	deleted chan *appsv1.ReplicaSet,
	errors chan error,
) *ReplicaSetInformer {
	r.ReplicaSetAdded = added
	r.ReplicaSetModified = modified
	r.ReplicaSetDeleted = deleted
	r.Errors = errors
	return r
}

func (r *ReplicaSetInformer) Run(ctx context.Context) (cleanupFn func(), err error) {
	var inform *util.Concurrent[*informer.Informer]
	if err := r.InformerFactory.RWTransactionErr(func(factory *informer.InformerFactory) error {
		inform, err = factory.ForNamespace(schema.GroupVersionResource{
			Group:    "apps",
			Version:  "v1",
			Resource: "replicasets",
		}, r.Namespace)
		if err != nil {
			return fmt.Errorf("get informer from factory: %w", err)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	labelSelector, err := metav1.LabelSelectorAsSelector(r.Controller.LabelSelector())
	if err != nil {
		return nil, fmt.Errorf("convert label selector: %w", err)
	}

	if err := inform.RWTransactionErr(func(inf *informer.Informer) error {
		handler, err := inf.AddEventHandler(
			cache.FilteringResourceEventHandler{
				FilterFunc: func(obj interface{}) bool {
					if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
						obj = d.Obj
					}

					rsObj := &appsv1.ReplicaSet{}
					lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, rsObj))
					return labelSelector.Matches(apilabels.Set(rsObj.GetLabels()))
				},
				Handler: cache.ResourceEventHandlerFuncs{
					AddFunc: func(obj interface{}) {
						if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
							obj = d.Obj
						}

						rsObj := &appsv1.ReplicaSet{}
						lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, rsObj))
						r.ReplicaSetAdded <- rsObj
					},
					UpdateFunc: func(oldObj, newObj interface{}) {
						if d, ok := newObj.(cache.DeletedFinalStateUnknown); ok {
							newObj = d.Obj
						}

						rsObj := &appsv1.ReplicaSet{}
						lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(newObj.(*unstructured.Unstructured).Object, rsObj))
						r.ReplicaSetModified <- rsObj
					},
					DeleteFunc: func(obj interface{}) {
						if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
							obj = d.Obj
						}

						rsObj := &appsv1.ReplicaSet{}
						lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, rsObj))
						r.ReplicaSetDeleted <- rsObj
					},
				},
			},
		)
		if err != nil {
			return fmt.Errorf("add event handler: %w", err)
		}

		cleanupFn = func() {
			inf.RemoveEventHandler(handler)
		}

		inf.Run()

		return nil
	}); err != nil {
		return nil, err
	}

	return cleanupFn, nil
}
