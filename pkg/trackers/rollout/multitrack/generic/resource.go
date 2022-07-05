package generic

import (
	"context"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/werf/kubedog/pkg/tracker/generic"
)

type Resource struct {
	Spec    *Spec
	State   *State
	Feed    *generic.Feed
	Context *Context
}

func NewResource(
	ctx context.Context,
	spec *Spec,
	client kubernetes.Interface,
	dynClient dynamic.Interface,
	discClient discovery.CachedDiscoveryInterface,
	mapper meta.RESTMapper,
) *Resource {
	tracker := generic.NewTracker(spec.ResourceID, client, dynClient, discClient, mapper)

	return &Resource{
		Spec:    spec,
		State:   NewState(),
		Feed:    generic.NewFeed(tracker),
		Context: NewContext(ctx),
	}
}
