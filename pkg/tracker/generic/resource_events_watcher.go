package generic

import (
	"context"
	"fmt"
	"sync"

	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	apilabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/tracker/debug"
	"github.com/werf/kubedog/pkg/tracker/resid"
	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
)

type ResourceEventsWatcher struct {
	ResourceID *resid.ResourceID

	object *unstructured.Unstructured

	resourceInitialEventsUIDsMux  sync.Mutex
	resourceInitialEventsUIDsList []types.UID

	client          kubernetes.Interface
	mapper          meta.RESTMapper
	informerFactory *util.Concurrent[*informer.InformerFactory]
}

func NewResourceEventsWatcher(
	object *unstructured.Unstructured,
	resID *resid.ResourceID,
	client kubernetes.Interface,
	mapper meta.RESTMapper,
	informerFactory *util.Concurrent[*informer.InformerFactory],
) *ResourceEventsWatcher {
	return &ResourceEventsWatcher{
		ResourceID:      resID,
		object:          object,
		client:          client,
		mapper:          mapper,
		informerFactory: informerFactory,
	}
}

func (i *ResourceEventsWatcher) Run(ctx context.Context, eventsCh chan<- *corev1.Event) (cleanupFn func(), err error) {
	namespaced, err := i.ResourceID.Namespaced(i.mapper)
	if err != nil {
		return nil, fmt.Errorf("check if resource is namespaced: %w", err)
	}

	var eventNamespace string
	if namespaced {
		eventNamespace = i.ResourceID.Namespace
	} else {
		eventNamespace = metav1.NamespaceDefault
	}

	var inform *util.Concurrent[*informer.Informer]
	if err := i.informerFactory.RWTransactionErr(func(factory *informer.InformerFactory) error {
		inform, err = factory.ForNamespace(schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "events",
		}, eventNamespace)
		if err != nil {
			return fmt.Errorf("get informer from factory: %w", err)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	involvedObjMetadata, err := meta.Accessor(i.object)
	if err != nil {
		return nil, fmt.Errorf("get involved object metadata: %w", err)
	}

	if err := inform.RWTransactionErr(func(inf *informer.Informer) error {
		if err := i.generateResourceInitialEventsUIDs(inf, string(involvedObjMetadata.GetUID())); err != nil {
			return fmt.Errorf("generate initial events uids: %w", err)
		}

		handler, err := inf.AddEventHandler(
			cache.FilteringResourceEventHandler{
				FilterFunc: func(obj interface{}) bool {
					eventObj := &corev1.Event{}
					lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, eventObj))
					return eventObj.InvolvedObject.UID == involvedObjMetadata.GetUID()
				},
				Handler: cache.ResourceEventHandlerFuncs{
					AddFunc: func(obj interface{}) {
						eventObj := &corev1.Event{}
						lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, eventObj))
						i.handleEventStateChange(ctx, eventObj, eventsCh)
					},
					UpdateFunc: func(oldObj, newObj interface{}) {
						eventObj := &corev1.Event{}
						lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(newObj.(*unstructured.Unstructured).Object, eventObj))
						i.handleEventStateChange(ctx, eventObj, eventsCh)
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

func (i *ResourceEventsWatcher) generateResourceInitialEventsUIDs(inform *informer.Informer, involvedUID string) error {
	objs, err := inform.List(apilabels.Everything())
	if err != nil {
		return fmt.Errorf("list events: %w", err)
	}

	var filteredEvents []*corev1.Event
	for _, obj := range objs {
		eventObj := &corev1.Event{}
		lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, eventObj))

		if string(eventObj.InvolvedObject.UID) == involvedUID {
			filteredEvents = append(filteredEvents, eventObj)
		}
	}

	for _, event := range filteredEvents {
		i.appendResourceInitialEventsUID(event.GetUID())
	}

	return nil
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
