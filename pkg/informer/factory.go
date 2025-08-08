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

type InformerFactory struct {
	clusteredFactory    dynamicinformer.DynamicSharedInformerFactory
	dynamicClient       dynamic.Interface
	informersLock       *sync.RWMutex
	namespacedFactories map[string]dynamicinformer.DynamicSharedInformerFactory
	stopCh              <-chan struct{}
	watchErrCh          chan<- error
}

func (f *InformerFactory) ForNamespace(gvr schema.GroupVersionResource, namespace string) (*util.Concurrent[*Informer], error) {
	factory, found := f.namespacedFactories[namespace]
	if !found {
		factory = dynamicinformer.NewFilteredDynamicSharedInformerFactory(f.dynamicClient, 0, namespace, nil)
		f.namespacedFactories[namespace] = factory
	}

	informer, err := newInformerFromFactory(gvr, factory, f.stopCh, f.watchErrCh, informerFromFactoryOptions{
		Namespace: namespace,
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
