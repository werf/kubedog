package generic

import (
	"context"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/werf/kubedog/pkg/tracker/debug"
	"github.com/werf/kubedog/pkg/tracker/resid"
	"github.com/werf/kubedog/pkg/utils"
)

type ResourceEventsWatcher struct {
	ResourceID *resid.ResourceID

	object *unstructured.Unstructured

	resourceInitialEventsUIDsMux  sync.Mutex
	resourceInitialEventsUIDsList []types.UID

	client kubernetes.Interface
}

func NewResourceEventsWatcher(
	object *unstructured.Unstructured,
	resID *resid.ResourceID,
	client kubernetes.Interface,
) *ResourceEventsWatcher {
	return &ResourceEventsWatcher{
		ResourceID: resID,
		object:     object,
		client:     client,
	}
}

func (i *ResourceEventsWatcher) Run(ctx context.Context, eventsCh chan<- *corev1.Event) error {
	runCtx, runCancelFn := context.WithCancelCause(ctx)
	defer runCancelFn(fmt.Errorf("context canceled: resource events watcher for %q finished", i.ResourceID))

	i.generateResourceInitialEventsUIDs(runCtx)

	fieldsSet, eventsNs := utils.EventFieldSelectorFromUnstructured(i.object)

	tweakListOptsFn := func(options *metav1.ListOptions) {
		options.FieldSelector = fieldsSet.AsSelector().String()
	}

	informer := cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				tweakListOptsFn(&options)
				return i.client.CoreV1().Events(eventsNs).List(runCtx, options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				tweakListOptsFn(&options)
				return i.client.CoreV1().Events(eventsNs).Watch(runCtx, options)
			},
		},
		&corev1.Event{},
		0,
		cache.Indexers{},
	)

	informer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				if debug.Debug() {
					fmt.Printf("    add event: %#v\n", i.ResourceID)
				}
				i.handleEventStateChange(runCtx, obj.(*corev1.Event), eventsCh)
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				if debug.Debug() {
					fmt.Printf("    update event: %#v\n", i.ResourceID)
				}
				i.handleEventStateChange(runCtx, newObj.(*corev1.Event), eventsCh)
			},
			DeleteFunc: func(obj interface{}) {
				if debug.Debug() {
					fmt.Printf("    delete event: %#v\n", i.ResourceID)
				}
			},
		},
	)

	if err := SetWatchErrorHandler(runCancelFn, i.ResourceID.String(), informer.SetWatchErrorHandler, SetWatchErrorHandlerOptions{}); err != nil {
		return fmt.Errorf("error setting watch error handler: %w", err)
	}

	informer.Run(runCtx.Done())

	return nil
}

func (i *ResourceEventsWatcher) generateResourceInitialEventsUIDs(ctx context.Context) {
	eventsList, err := utils.ListEventsForUnstructured(ctx, i.client, i.object)
	if err != nil {
		if debug.Debug() {
			fmt.Printf("list event error: %v\n", err)
		}
		return
	}

	for _, event := range eventsList.Items {
		i.appendResourceInitialEventsUID(event.GetUID())
	}
}

func (i *ResourceEventsWatcher) handleEventStateChange(ctx context.Context, eventObj *corev1.Event, eventsCh chan<- *corev1.Event) {
	for _, uid := range i.resourceInitialEventsUIDs() {
		if uid != eventObj.GetUID() {
			continue
		}

		i.deleteResourceInitialEventsUID(uid)

		return
	}

	if debug.Debug() {
		fmt.Printf("  %s got normal event: %s: %s\n", i.ResourceID, eventObj.Reason, eventObj.Message)
	}

	eventsCh <- eventObj
}

func (i *ResourceEventsWatcher) resourceInitialEventsUIDs() []types.UID {
	i.resourceInitialEventsUIDsMux.Lock()
	defer i.resourceInitialEventsUIDsMux.Unlock()

	return i.resourceInitialEventsUIDsList
}

func (i *ResourceEventsWatcher) appendResourceInitialEventsUID(uid types.UID) {
	i.resourceInitialEventsUIDsMux.Lock()
	defer i.resourceInitialEventsUIDsMux.Unlock()

	i.resourceInitialEventsUIDsList = append(i.resourceInitialEventsUIDsList, uid)
}

func (i *ResourceEventsWatcher) deleteResourceInitialEventsUID(uid types.UID) {
	i.resourceInitialEventsUIDsMux.Lock()
	defer i.resourceInitialEventsUIDsMux.Unlock()

	var result []types.UID
	for _, u := range i.resourceInitialEventsUIDsList {
		if u == uid {
			continue
		}

		result = append(result, u)
	}

	i.resourceInitialEventsUIDsList = result
}
