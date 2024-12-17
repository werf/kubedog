package generic

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/werf/kubedog-for-werf-helm/pkg/tracker/debug"
	"github.com/werf/kubedog-for-werf-helm/pkg/tracker/resid"
)

type ResourceStateWatcher struct {
	ResourceID *resid.ResourceID

	client        kubernetes.Interface
	dynamicClient dynamic.Interface
	mapper        meta.RESTMapper
}

func NewResourceStateWatcher(
	resID *resid.ResourceID,
	client kubernetes.Interface,
	dynClient dynamic.Interface,
	mapper meta.RESTMapper,
) *ResourceStateWatcher {
	return &ResourceStateWatcher{
		ResourceID:    resID,
		client:        client,
		dynamicClient: dynClient,
		mapper:        mapper,
	}
}

func (w *ResourceStateWatcher) Run(ctx context.Context, resourceAddedCh, resourceModifiedCh, resourceDeletedCh chan<- *unstructured.Unstructured) error {
	runCtx, runCancelFn := context.WithCancel(ctx)
	defer runCancelFn()

	gvr, err := w.ResourceID.GroupVersionResource(w.mapper)
	if err != nil {
		return fmt.Errorf("error getting GroupVersionResource: %w", err)
	}

	informer := dynamicinformer.NewFilteredDynamicInformer(
		w.dynamicClient,
		*gvr,
		w.ResourceID.Namespace,
		0,
		cache.Indexers{},
		func(options *metav1.ListOptions) {
			options.FieldSelector = fields.OneTermEqualSelector("metadata.name", w.ResourceID.Name).String()
		},
	)

	informer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				if debug.Debug() {
					fmt.Printf("    add state event: %#v\n", w.ResourceID)
				}
				resourceAddedCh <- obj.(*unstructured.Unstructured)
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				if debug.Debug() {
					fmt.Printf("    update state event: %#v\n", w.ResourceID)
				}
				resourceModifiedCh <- newObj.(*unstructured.Unstructured)
			},
			DeleteFunc: func(obj interface{}) {
				if debug.Debug() {
					fmt.Printf("    delete state event: %#v\n", w.ResourceID)
				}
				resourceDeletedCh <- obj.(*unstructured.Unstructured)
			},
		},
	)

	fatalWatchErr := &UnrecoverableWatchError{}
	if err := SetWatchErrorHandler(runCancelFn, w.ResourceID.String(), informer.Informer().SetWatchErrorHandler, SetWatchErrorHandlerOptions{FatalWatchErr: fatalWatchErr}); err != nil {
		return fmt.Errorf("error setting watch error handler: %w", err)
	}

	if debug.Debug() {
		fmt.Printf("      %s resource watcher STARTED\n", w.ResourceID)
	}

	informer.Informer().Run(runCtx.Done())

	if debug.Debug() {
		fmt.Printf("      %s resource watcher DONE\n", w.ResourceID)
	}

	if fatalWatchErr.Err != nil {
		return fatalWatchErr
	} else {
		return nil
	}
}
