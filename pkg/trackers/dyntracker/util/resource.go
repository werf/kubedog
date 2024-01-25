package util

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func ResourceID(name, namespace string, groupVersionKind schema.GroupVersionKind) string {
	return fmt.Sprintf("%s:%s:%s:%s", namespace, groupVersionKind.Group, groupVersionKind.Kind, name)
}

func IsNamespaced(groupVersionKind schema.GroupVersionKind, mapper meta.ResettableRESTMapper) (namespaced bool, err error) {
	mapping, err := mapper.RESTMapping(groupVersionKind.GroupKind(), groupVersionKind.Version)
	if err != nil {
		return false, fmt.Errorf("get resource mapping for %q: %w", groupVersionKind.String(), err)
	}

	return mapping.Scope == meta.RESTScopeNamespace, nil
}

func GVRFromGVK(groupVersionKind schema.GroupVersionKind, mapper meta.ResettableRESTMapper) (schema.GroupVersionResource, error) {
	mapping, err := mapper.RESTMapping(groupVersionKind.GroupKind(), groupVersionKind.Version)
	if err != nil {
		return schema.GroupVersionResource{}, fmt.Errorf("get resource mapping for %q: %w", groupVersionKind.String(), err)
	}

	return mapping.Resource, nil
}

func ResourceHumanID(name, namespace string, groupVersionKind schema.GroupVersionKind, mapper meta.ResettableRESTMapper) string {
	namespaced := true
	if mapper != nil {
		if nsed, err := IsNamespaced(groupVersionKind, mapper); err == nil {
			namespaced = nsed
		}
	}

	if namespaced && namespace != "" {
		return fmt.Sprintf("%s/%s/%s", namespace, groupVersionKind.Kind, name)
	} else {
		return fmt.Sprintf("%s/%s", groupVersionKind.Kind, name)
	}
}
