package pod

import (
	"context"
	"fmt"

	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	apilabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"

	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
	"github.com/werf/kubedog/pkg/utils"
)

// PodsInformer monitor pod add events to use with controllers (Deployment, StatefulSet, DaemonSet)
type PodsInformer struct {
	tracker.Tracker
	Controller utils.ControllerMetadata
	PodAdded   chan *corev1.Pod
	Errors     chan error
}

func NewPodsInformer(trk *tracker.Tracker, controller utils.ControllerMetadata) *PodsInformer {
	return &PodsInformer{
		Tracker: tracker.Tracker{
			Kube:             trk.Kube,
			Namespace:        trk.Namespace,
			FullResourceName: trk.FullResourceName,
			ResourceName:     trk.ResourceName,
			InformerFactory:  trk.InformerFactory,
		},
		Controller: controller,
		PodAdded:   make(chan *corev1.Pod, 1),
		Errors:     make(chan error, 1),
	}
}

func (p *PodsInformer) WithChannels(added chan *corev1.Pod, errors chan error) *PodsInformer {
	p.PodAdded = added
	p.Errors = errors
	return p
}

func (p *PodsInformer) Run(ctx context.Context) (cleanupFn func(), err error) {
	var inform *util.Concurrent[*informer.Informer]
	if err := p.InformerFactory.RWTransactionErr(func(factory *informer.InformerFactory) error {
		inform, err = factory.ForNamespace(schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "pods",
		}, p.Namespace)
		if err != nil {
			return fmt.Errorf("get informer from factory: %w", err)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	labelSelector, err := metav1.LabelSelectorAsSelector(p.Controller.LabelSelector())
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

					podObj := &corev1.Pod{}
					lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, podObj))
					return labelSelector.Matches(apilabels.Set(podObj.GetLabels()))
				},
				Handler: cache.ResourceEventHandlerFuncs{
					AddFunc: func(obj interface{}) {
						if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
							obj = d.Obj
						}

						podObj := &corev1.Pod{}
						lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, podObj))
						p.PodAdded <- podObj
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
