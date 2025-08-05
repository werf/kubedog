package informer

import (
	"fmt"
	"io"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"

	"github.com/werf/kubedog/pkg/tracker/debug"
)

type informerFromFactoryOptions struct {
	Namespace string
}

func newInformerFromFactory(gvr schema.GroupVersionResource, factory dynamicinformer.DynamicSharedInformerFactory, stopCh <-chan struct{}, watchErrCh chan<- error, opts informerFromFactoryOptions) (*Informer, error) {
	informer := factory.ForResource(gvr)

	if err := setWatchErrorHandler(informer.Informer().SetWatchErrorHandler, watchErrCh, gvr); err != nil {
		return nil, fmt.Errorf("set watch error handler for resource %s: %w", gvr.String(), err)
	}

	return &Informer{
		factory:   factory,
		informer:  informer.Informer(),
		lister:    informer.Lister(),
		namespace: opts.Namespace,
		stopCh:    stopCh,
	}, nil
}

type Informer struct {
	factory   dynamicinformer.DynamicSharedInformerFactory
	informer  cache.SharedIndexInformer
	lister    cache.GenericLister
	namespace string
	stopCh    <-chan struct{}
}

func (i *Informer) AddEventHandler(handler cache.ResourceEventHandler) (cache.ResourceEventHandlerRegistration, error) {
	return i.informer.AddEventHandler(handler)
}

func (i *Informer) RemoveEventHandler(handle cache.ResourceEventHandlerRegistration) error {
	return i.informer.RemoveEventHandler(handle)
}

func (i *Informer) Run() {
	i.factory.Start(i.stopCh)
}

func (i *Informer) List(selector labels.Selector) (ret []runtime.Object, err error) {
	if i.namespace != "" {
		return i.lister.ByNamespace(i.namespace).List(selector)
	}

	return i.lister.List(selector)
}

func (i *Informer) Get(name string) (runtime.Object, error) {
	if i.namespace != "" {
		return i.lister.ByNamespace(i.namespace).Get(name)
	}

	return i.lister.Get(name)
}

func setWatchErrorHandler(setWatchErrorHandler func(handler cache.WatchErrorHandler) error, watchErrCh chan<- error, gvr schema.GroupVersionResource) error {
	if err := setWatchErrorHandler(
		func(r *cache.Reflector, err error) {
			isExpiredError := func(err error) bool {
				return apierrors.IsResourceExpired(err) || apierrors.IsGone(err)
			}

			// Based on: k8s.io/client-go@v0.30.11/tools/cache/reflector.go
			switch {
			case isExpiredError(err):
				if debug.Debug() {
					fmt.Printf("[SetWatchErrorHandler] %s watch closed with expired error: %s\n", gvr.String(), err)
				}
			case err == io.EOF:
				// watch closed normally
			case err == io.ErrUnexpectedEOF:
				if debug.Debug() {
					fmt.Printf("[SetWatchErrorHandler] %s watch closed with unexpected EOF error: %s\n", gvr.String(), err)
				}
			default:
				if debug.Debug() {
					fmt.Printf("[SetWatchErrorHandler] %s watch closed with an error: %s\n", gvr.String(), err)
				}

				watchErrCh <- fmt.Errorf("unrecoverable watch error for %s: %w", gvr.String(), err)
			}
		},
	); err != nil {
		if err.Error() == "informer has already started" {
			return nil
		}

		return err
	}

	return nil
}
