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
	watchtools "k8s.io/client-go/tools/watch"

	"github.com/werf/kubedog/pkg/tracker"
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
	i.generateResourceInitialEventsUIDs(ctx)

	fieldsSet, eventsNs := utils.EventFieldSelectorFromUnstructured(i.object)

	setOptionsFunc := func(options *metav1.ListOptions) {
		options.FieldSelector = fieldsSet.AsSelector().String()
	}

	listWatch := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			setOptionsFunc(&options)
			return i.client.CoreV1().Events(eventsNs).List(ctx, options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			setOptionsFunc(&options)
			return i.client.CoreV1().Events(eventsNs).Watch(ctx, options)
		},
	}

	if debug.Debug() {
		fmt.Printf("> %s run event informer\n", i.ResourceID)
	}

	_, err := watchtools.UntilWithSync(ctx, listWatch, &corev1.Event{}, nil, func(watchEvent watch.Event) (bool, error) {
		if debug.Debug() {
			fmt.Printf("    %s event: %#v\n", i.ResourceID, watchEvent.Type)
		}

		var event *corev1.Event
		if watchEvent.Type != watch.Error {
			var ok bool
			event, ok = watchEvent.Object.(*corev1.Event)
			if !ok {
				return true, fmt.Errorf("TRACK EVENT expect *corev1.Event object, got %T", watchEvent.Object)
			}
		}

		switch watchEvent.Type {
		case watch.Added, watch.Modified:
			i.handleEventStateChange(ctx, event, eventsCh)
		case watch.Deleted:
		case watch.Error:
			return true, fmt.Errorf("event watch error: %v", watchEvent.Object)
		}

		return false, nil
	})

	if debug.Debug() {
		fmt.Printf("     %s event informer DONE\n", i.ResourceID)
	}

	return tracker.AdaptInformerError(err)
}

func (i *ResourceEventsWatcher) generateResourceInitialEventsUIDs(ctx context.Context) {
	eventsList, err := utils.ListEventsForUnstructured(ctx, i.client, i.object)
	if err != nil {
		if debug.Debug() {
			fmt.Printf("list event error: %v\n", err)
		}
		return
	}

	if debug.Debug() {
		utils.DescribeEvents(eventsList)
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

		if debug.Debug() {
			fmt.Printf("IGNORE initial event: %s: %s\n", eventObj.Reason, eventObj.Message)
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
