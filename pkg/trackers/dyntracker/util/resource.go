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

func ResourceHumanID(name, namespace string, groupVersionKind schema.GroupVersionKind, mapper meta.ResettableRESTMapper) (string, error) {
	namespaced, err := IsNamespaced(groupVersionKind, mapper)
	if err != nil {
		return "", fmt.Errorf("check if namespaced: %w", err)
	}

	if namespaced && namespace != "" {
		return fmt.Sprintf("%s/%s/%s", namespace, groupVersionKind.Kind, name), nil
	} else {
		return fmt.Sprintf("%s/%s", groupVersionKind.Kind, name), nil
	}
}
