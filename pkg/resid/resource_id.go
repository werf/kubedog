package resid

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type ResourceID struct {
	Name             string
	Namespace        string
	GroupVersionKind schema.GroupVersionKind
}

type NewResourceIDOptions struct {
	Namespace string
}

func NewResourceID(name string, gvk schema.GroupVersionKind, options NewResourceIDOptions) *ResourceID {
	return &ResourceID{
		Name:             name,
		GroupVersionKind: gvk,
		Namespace:        options.Namespace,
	}
}

func (r *ResourceID) String() string {
	return r.GroupVersionKindNamespaceNameString()
}

func (r *ResourceID) GroupVersionKindString() string {
	var gvkElems []string
	if r.GroupVersionKind.Kind != "" {
		gvkElems = append(gvkElems, r.GroupVersionKind.Kind)
	}

	if r.GroupVersionKind.Version != "" {
		gvkElems = append(gvkElems, r.GroupVersionKind.Version)
	}

	if r.GroupVersionKind.Group != "" {
		gvkElems = append(gvkElems, r.GroupVersionKind.Group)
	}

	return strings.Join(gvkElems, ".")
}

func (r *ResourceID) GroupVersionKindNameString() string {
	return strings.Join([]string{r.GroupVersionKindString(), r.Name}, "/")
}

func (r *ResourceID) KindNameString() string {
	return strings.Join([]string{r.GroupVersionKind.Kind, r.Name}, "/")
}

func (r *ResourceID) GroupVersionKindNamespaceString() string {
	var resultElems []string

	if r.Namespace != "" {
		resultElems = append(resultElems, fmt.Sprint("ns:", r.Namespace))
	}

	gvk := r.GroupVersionKindString()
	if gvk != "" {
		resultElems = append(resultElems, gvk)
	}

	return strings.Join(resultElems, "/")
}

func (r *ResourceID) GroupVersionKindNamespaceNameString() string {
	return strings.Join([]string{r.GroupVersionKindNamespaceString(), r.Name}, "/")
}

func (r *ResourceID) GroupVersionResource(mapper meta.RESTMapper) (*schema.GroupVersionResource, error) {
	mapping, err := r.mapping(mapper)
	if err != nil {
		return nil, fmt.Errorf("error getting mapping: %w", err)
	}

	return &mapping.Resource, nil
}

func (r *ResourceID) Namespaced(mapper meta.RESTMapper) (bool, error) {
	mapping, err := r.mapping(mapper)
	if err != nil {
		return false, fmt.Errorf("error getting mapping: %w", err)
	}

	return mapping.Scope.Name() == meta.RESTScopeNameNamespace, nil
}

func (r *ResourceID) mapping(mapper meta.RESTMapper) (*meta.RESTMapping, error) {
	mapping, err := mapper.RESTMapping(r.GroupVersionKind.GroupKind(), r.GroupVersionKind.Version)
	if err != nil {
		return nil, fmt.Errorf("error mapping %q to api resource: %w", r.GroupVersionKindString(), err)
	}

	return mapping, nil
}
