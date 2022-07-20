package generic

import (
	"context"
	"fmt"

	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"

	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/tracker/debug"
	"github.com/werf/kubedog/pkg/tracker/resid"
	"github.com/werf/logboek"
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
	gvr, err := w.ResourceID.GroupVersionResource(w.mapper)
	if err != nil {
		return fmt.Errorf("error getting GroupVersionResource: %w", err)
	}

	for _, verb := range []string{"list", "watch"} {
		if response, err := w.client.AuthorizationV1().SelfSubjectAccessReviews().Create(
			ctx,
			&authorizationv1.SelfSubjectAccessReview{
				Spec: authorizationv1.SelfSubjectAccessReviewSpec{
					ResourceAttributes: &authorizationv1.ResourceAttributes{
						Verb:      verb,
						Resource:  gvr.Resource,
						Namespace: w.ResourceID.Namespace,
						Group:     w.ResourceID.GroupVersionKind.Group,
						Version:   w.ResourceID.GroupVersionKind.Version,
						Name:      w.ResourceID.Name,
					},
				},
			},
			metav1.CreateOptions{},
		); err != nil {
			logboek.Context(context.Background()).Warn().LogF("Won't track %q: error checking %q access: %s\n", w.ResourceID, verb, err)
			return nil
		} else if !response.Status.Allowed {
			logboek.Context(context.Background()).Warn().LogF("Won't track %q: no %q access.\n", w.ResourceID, verb)
			return nil
		}
	}

	resClient, err := w.resourceClient()
	if err != nil {
		return fmt.Errorf("error getting resource client: %w", err)
	}

	setOptionsFunc := func(options *metav1.ListOptions) {
		options.FieldSelector = fields.OneTermEqualSelector("metadata.name", w.ResourceID.Name).String()
	}

	listWatch := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			setOptionsFunc(&options)
			return resClient.List(ctx, options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			setOptionsFunc(&options)
			return resClient.Watch(ctx, options)
		},
	}

	_, err = watchtools.UntilWithSync(ctx, listWatch, &unstructured.Unstructured{}, nil,
		func(event watch.Event) (bool, error) {
			if debug.Debug() {
				fmt.Printf("    %s event: %#v\n", w.ResourceID, event.Type)
			}

			switch event.Type {
			case watch.Added:
				resourceAddedCh <- event.Object.(*unstructured.Unstructured)
			case watch.Modified:
				resourceModifiedCh <- event.Object.(*unstructured.Unstructured)
			case watch.Deleted:
				resourceDeletedCh <- event.Object.(*unstructured.Unstructured)
			case watch.Error:
				return true, fmt.Errorf("watch error: %v", event.Object)
			}

			return false, nil
		},
	)

	if debug.Debug() {
		fmt.Printf("      %s resource watcher DONE\n", w.ResourceID)
	}

	return tracker.AdaptInformerError(err)
}

func (w *ResourceStateWatcher) resourceClient() (dynamic.ResourceInterface, error) {
	gvr, err := w.ResourceID.GroupVersionResource(w.mapper)
	if err != nil {
		return nil, fmt.Errorf("error getting GroupVersionResource: %w", err)
	}

	resClient := w.dynamicClient.Resource(*gvr)

	if namespaced, err := w.ResourceID.Namespaced(w.mapper); err != nil {
		return nil, fmt.Errorf("error checking whether resource is namespaced: %w", err)
	} else if namespaced {
		resClient.Namespace(w.ResourceID.Namespace)
	}

	return resClient, nil
}
