package resource

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"

	"github.com/werf/kubedog/pkg/dyntracker/util"
	"github.com/werf/kubedog/pkg/informer"
)

type EventType string

const (
	EventAdded    EventType = "Added"
	EventModified EventType = "Modified"
	EventDeleted  EventType = "Deleted"
	// EventSynced is delivered once, after the handler received all the objects that
	// existed when the watch started. Object is nil for this event.
	EventSynced EventType = "Synced"
)

type ObjectEvent[T any] struct {
	Type   EventType
	Object *T
}

type WatchOptions struct {
	// Namespace to watch, all namespaces (or cluster-scoped resources) if empty.
	Namespace string
	// Name, if set, makes the watch receive only the object with this name.
	Name string
	// Selector, if set, makes the watch receive only the objects with matching labels.
	Selector labels.Selector

	InformerOptions informer.InformerOptions
}

// Watch subscribes the handler to the objects of the resource in the shared informers of
// the factory. Objects are converted from unstructured into T, conversion errors are passed
// to onError. The handler is called from the informer goroutines, so it should only hand the
// event over to the tracker, e.g. by sending it to a channel.
func Watch[T any](
	ctx context.Context,
	informerFactory *util.Concurrent[*informer.InformerFactory],
	gvr schema.GroupVersionResource,
	opts WatchOptions,
	handler func(ObjectEvent[T]),
	onError func(error),
) (cleanupFn func(), err error) {
	var inform *util.Concurrent[*informer.Informer]
	if err := informerFactory.RWTransactionErr(func(factory *informer.InformerFactory) error {
		inform, err = factory.ForNamespace(gvr, opts.Namespace, opts.InformerOptions)
		if err != nil {
			return fmt.Errorf("get informer from factory: %w", err)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	handle := func(eventType EventType, obj interface{}) {
		object, err := convert[T](obj)
		if err != nil {
			onError(fmt.Errorf("convert %s: %w", gvr.String(), err))
			return
		}

		handler(ObjectEvent[T]{Type: eventType, Object: object})
	}

	var registration cache.ResourceEventHandlerRegistration
	if err := inform.RWTransactionErr(func(inf *informer.Informer) error {
		registration, err = inf.AddEventHandler(
			cache.FilteringResourceEventHandler{
				FilterFunc: func(obj interface{}) bool {
					return matches(obj, opts)
				},
				Handler: cache.ResourceEventHandlerFuncs{
					AddFunc: func(obj interface{}) {
						handle(EventAdded, obj)
					},
					UpdateFunc: func(_, newObj interface{}) {
						handle(EventModified, newObj)
					},
					DeleteFunc: func(obj interface{}) {
						handle(EventDeleted, obj)
					},
				},
			},
		)
		if err != nil {
			return fmt.Errorf("add event handler: %w", err)
		}

		inf.Run()

		return nil
	}); err != nil {
		return nil, err
	}

	syncCtx, cancelSync := context.WithCancel(ctx)
	go func() {
		// The registration is synced only after the handler returned for all the initial
		// objects, so the Synced event always comes after them.
		if cache.WaitForCacheSync(syncCtx.Done(), registration.HasSynced) {
			handler(ObjectEvent[T]{Type: EventSynced})
		}
	}()

	cleanupFn = func() {
		cancelSync()
		_ = inform.RWTransactionErr(func(inf *informer.Informer) error {
			return inf.RemoveEventHandler(registration)
		})
	}

	return cleanupFn, nil
}

func matches(obj interface{}, opts WatchOptions) bool {
	unstruct, ok := unwrap(obj)
	if !ok {
		return false
	}

	if opts.Name != "" && unstruct.GetName() != opts.Name {
		return false
	}

	if opts.Namespace != "" && unstruct.GetNamespace() != opts.Namespace {
		return false
	}

	if opts.Selector != nil && !opts.Selector.Matches(labels.Set(unstruct.GetLabels())) {
		return false
	}

	return true
}

func convert[T any](obj interface{}) (*T, error) {
	unstruct, ok := unwrap(obj)
	if !ok {
		return nil, fmt.Errorf("unexpected object type %T", obj)
	}

	object := new(T)
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstruct.Object, object); err != nil {
		return nil, fmt.Errorf("convert from unstructured: %w", err)
	}

	return object, nil
}

func unwrap(obj interface{}) (*unstructured.Unstructured, bool) {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}

	unstruct, ok := obj.(*unstructured.Unstructured)

	return unstruct, ok
}
