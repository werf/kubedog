package generic

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/tracker/resid"
	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
)

type ResourceStateWatcher struct {
	ResourceID *resid.ResourceID

	dynamicClient   dynamic.Interface
	mapper          meta.RESTMapper
	informerFactory *util.Concurrent[*informer.InformerFactory]
}

func NewResourceStateWatcher(
	resID *resid.ResourceID,
	dynClient dynamic.Interface,
	mapper meta.RESTMapper,
	informerFactory *util.Concurrent[*informer.InformerFactory],
) *ResourceStateWatcher {
	return &ResourceStateWatcher{
		ResourceID:      resID,
		dynamicClient:   dynClient,
		mapper:          mapper,
		informerFactory: informerFactory,
	}
}

func (w *ResourceStateWatcher) Run(ctx context.Context, resourceAddedCh, resourceModifiedCh, resourceDeletedCh chan<- *unstructured.Unstructured) (cleanupFn func(), err error) {
	gvr, err := w.ResourceID.GroupVersionResource(w.mapper)
	if err != nil {
		return nil, fmt.Errorf("get GroupVersionResource: %w", err)
	}

	namespaced, err := w.ResourceID.Namespaced(w.mapper)
	if err != nil {
		return nil, fmt.Errorf("check if resource is namespaced: %w", err)
	}

	var inform *util.Concurrent[*informer.Informer]
	if err := w.informerFactory.RWTransactionErr(func(factory *informer.InformerFactory) error {
		if namespaced {
			inform, err = factory.ForNamespace(*gvr, w.ResourceID.Namespace)
			if err != nil {
				return fmt.Errorf("get namespaced informer from factory: %w", err)
			}
		} else {
			inform, err = factory.Clustered(*gvr)
			if err != nil {
				return fmt.Errorf("get clustered informer from factory: %w", err)
			}
		}

		return nil
	}); err != nil {
		return nil, err
	}

	if err := inform.RWTransactionErr(func(inf *informer.Informer) error {
		handler, err := inf.AddEventHandler(
			cache.FilteringResourceEventHandler{
				FilterFunc: func(obj interface{}) bool {
					if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
						obj = d.Obj
					}

					unstructObj := obj.(*unstructured.Unstructured)
					return unstructObj.GetName() == w.ResourceID.Name &&
						(!namespaced || unstructObj.GetNamespace() == w.ResourceID.Namespace)
				},
				Handler: cache.ResourceEventHandlerFuncs{
					AddFunc: func(obj interface{}) {
						if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
							obj = d.Obj
						}

						resourceAddedCh <- obj.(*unstructured.Unstructured)
					},
					UpdateFunc: func(oldObj, newObj interface{}) {
						if d, ok := newObj.(cache.DeletedFinalStateUnknown); ok {
							newObj = d.Obj
						}

						resourceModifiedCh <- newObj.(*unstructured.Unstructured)
					},
					DeleteFunc: func(obj interface{}) {
						if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
							obj = d.Obj
						}

						resourceDeletedCh <- obj.(*unstructured.Unstructured)
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
