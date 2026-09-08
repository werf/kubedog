package informer

import (
	"fmt"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"

	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
)

type ConcurrentInformerFactoryOptions struct{}

func NewConcurrentInformerFactory(stopCh <-chan struct{}, watchErrCh chan<- error, dynamicClient dynamic.Interface, opts ConcurrentInformerFactoryOptions) *util.Concurrent[*InformerFactory] {
	lock := &sync.RWMutex{}
	return util.NewConcurrentWithLock(&InformerFactory{
		dynamicClient:       dynamicClient,
		namespacedFactories: make(map[string]dynamicinformer.DynamicSharedInformerFactory),
		informersLock:       lock,
		stopCh:              stopCh,
		watchErrCh:          watchErrCh,
	}, lock)
}

// InformerOptions are the settings of a particular informer.
type InformerOptions struct {
	// ForbiddenIsNotFatal makes "403 Forbidden" on list/watch a non-fatal error. The
	// informer keeps retrying, but the missing access doesn't stop the tracking.
	ForbiddenIsNotFatal bool
}

type InformerFactory struct {
	clusteredFactory    dynamicinformer.DynamicSharedInformerFactory
	dynamicClient       dynamic.Interface
	informersLock       *sync.RWMutex
	namespacedFactories map[string]dynamicinformer.DynamicSharedInformerFactory
	stopCh              <-chan struct{}
	watchErrCh          chan<- error
}

func (f *InformerFactory) ForNamespace(gvr schema.GroupVersionResource, namespace string, opts ...InformerOptions) (*util.Concurrent[*Informer], error) {
	var opt InformerOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	key := namespacedFactoryKey(namespace, opt)

	factory, found := f.namespacedFactories[key]
	if !found {
		factory = dynamicinformer.NewFilteredDynamicSharedInformerFactory(f.dynamicClient, 0, namespace, nil)
		f.namespacedFactories[key] = factory
	}

	informer, err := newInformerFromFactory(gvr, factory, f.stopCh, f.watchErrCh, informerFromFactoryOptions{
		Namespace:           namespace,
		ForbiddenIsNotFatal: opt.ForbiddenIsNotFatal,
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

	informer, err := newInformerFromFactory(gvr, f.clusteredFactory, f.stopCh, f.watchErrCh, informerFromFactoryOptions{})
	if err != nil {
		return nil, fmt.Errorf("construct informer: %w", err)
	}

	return util.NewConcurrentWithLock(informer, f.informersLock), nil
}

// namespacedFactoryKey makes the watch error policy a part of the informer identity: a
// single SharedIndexInformer has a single watch error handler, which can only be set once,
// so the consumers disagreeing on the policy must not share an informer. A namespace name
// can't contain a slash, hence the key never collides with a plain namespace.
func namespacedFactoryKey(namespace string, opt InformerOptions) string {
	if opt.ForbiddenIsNotFatal {
		return namespace + "/forbidden-is-not-fatal"
	}

	return namespace
}
