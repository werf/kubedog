package informer

import (
	"fmt"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"

	"github.com/werf/kubedog/pkg/display"
	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
)

type ConcurrentInformerFactoryOptions struct {
	// OnNonFatalWatchError reports a watch error that doesn't stop the tracking. It is
	// called at most once per informer. Defaults to printing a warning.
	OnNonFatalWatchError func(gvr schema.GroupVersionResource, namespace string, err error)
}

func NewConcurrentInformerFactory(stopCh <-chan struct{}, watchErrCh chan<- error, dynamicClient dynamic.Interface, opts ConcurrentInformerFactoryOptions) *util.Concurrent[*InformerFactory] {
	onNonFatalWatchError := opts.OnNonFatalWatchError
	if onNonFatalWatchError == nil {
		onNonFatalWatchError = warnAboutNonFatalWatchError
	}

	lock := &sync.RWMutex{}
	return util.NewConcurrentWithLock(&InformerFactory{
		dynamicClient:        dynamicClient,
		namespacedFactories:  make(map[namespacedFactoryKey]dynamicinformer.DynamicSharedInformerFactory),
		informersLock:        lock,
		stopCh:               stopCh,
		watchErrCh:           watchErrCh,
		onNonFatalWatchError: onNonFatalWatchError,
	}, lock)
}

func warnAboutNonFatalWatchError(gvr schema.GroupVersionResource, namespace string, err error) {
	display.ErrF("WARNING: no access to %s in namespace %q, tracking continues without it: %s\n", gvr.String(), namespace, err)
}

// InformerOptions are the settings of a particular informer.
type InformerOptions struct {
	// ForbiddenIsNotFatal makes "403 Forbidden" on list/watch a non-fatal error. The
	// informer keeps retrying, but the missing access doesn't stop the tracking.
	ForbiddenIsNotFatal bool
}

// namespacedFactoryKey makes the options a part of the informer identity: a single
// informer has a single watch error handler, which can only be set once, so the consumers
// disagreeing on the options must not share an informer. Informers are created lazily per
// resource, so a second factory for a namespace costs nothing until the same resource is
// actually requested with both options.
type namespacedFactoryKey struct {
	namespace string
	options   InformerOptions
}

type InformerFactory struct {
	clusteredFactory     dynamicinformer.DynamicSharedInformerFactory
	dynamicClient        dynamic.Interface
	informersLock        *sync.RWMutex
	namespacedFactories  map[namespacedFactoryKey]dynamicinformer.DynamicSharedInformerFactory
	stopCh               <-chan struct{}
	watchErrCh           chan<- error
	onNonFatalWatchError func(gvr schema.GroupVersionResource, namespace string, err error)
}

func (f *InformerFactory) ForNamespace(gvr schema.GroupVersionResource, namespace string, opts ...InformerOptions) (*util.Concurrent[*Informer], error) {
	if len(opts) > 1 {
		return nil, fmt.Errorf("expected at most one InformerOptions, got %d", len(opts))
	}

	var opt InformerOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	key := namespacedFactoryKey{namespace: namespace, options: opt}

	factory, found := f.namespacedFactories[key]
	if !found {
		factory = dynamicinformer.NewFilteredDynamicSharedInformerFactory(f.dynamicClient, 0, namespace, nil)
		f.namespacedFactories[key] = factory
	}

	informer, err := newInformerFromFactory(gvr, factory, f.stopCh, f.watchErrCh, informerFromFactoryOptions{
		Namespace:            namespace,
		ForbiddenIsNotFatal:  opt.ForbiddenIsNotFatal,
		OnNonFatalWatchError: f.onNonFatalWatchError,
	})
	if err != nil {
		return nil, fmt.Errorf("construct informer: %w", err)
	}

	return util.NewConcurrentWithLock(informer, f.informersLock), nil
}

func (f *InformerFactory) Clustered(gvr schema.GroupVersionResource) (*util.Concurrent[*Informer], error) {
	if f.clusteredFactory == nil {
		f.clusteredFactory = dynamicinformer.NewFilteredDynamicSharedInformerFactory(f.dynamicClient, 0, metav1.NamespaceAll, nil)
	}

	informer, err := newInformerFromFactory(gvr, f.clusteredFactory, f.stopCh, f.watchErrCh, informerFromFactoryOptions{
		OnNonFatalWatchError: f.onNonFatalWatchError,
	})
	if err != nil {
		return nil, fmt.Errorf("construct informer: %w", err)
	}

	return util.NewConcurrentWithLock(informer, f.informersLock), nil
}
