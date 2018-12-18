package utils

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Errors that could be returned by GetReference.
var (
	ErrNilObject  = errors.New("can't reference a nil object")
	ErrNoSelfLink = errors.New("selfLink was empty, can't make reference")
)

var scheme *runtime.Scheme = runtime.NewScheme()

func GetReference(obj runtime.Object) (*corev1.ObjectReference, error) {

	if obj == nil {
		return nil, ErrNilObject
	}
	if ref, ok := obj.(*corev1.ObjectReference); ok {
		// Don't make a reference to a reference.
		return ref, nil
	}

	gvk := obj.GetObjectKind().GroupVersionKind()

	// if the object referenced is actually persisted, we can just get kind from meta
	// if we are building an object reference to something not yet persisted, we should fallback to scheme
	kind := gvk.Kind
	if len(kind) == 0 {
		// TODO: this is wrong
		gvks, _, err := scheme.ObjectKinds(obj)
		if err != nil {
			return nil, err
		}
		kind = gvks[0].Kind
	}

	// An object that implements only List has enough metadata to build a reference
	var listMeta metav1.Common
	objectMeta, err := meta.Accessor(obj)
	if err != nil {
		listMeta, err = meta.CommonAccessor(obj)
		if err != nil {
			return nil, err
		}
	} else {
		listMeta = objectMeta
	}

	// if the object referenced is actually persisted, we can also get version from meta
	version := gvk.GroupVersion().String()
	if len(version) == 0 {
		selfLink := listMeta.GetSelfLink()
		if len(selfLink) == 0 {
			return nil, ErrNoSelfLink
		}
		selfLinkURL, err := url.Parse(selfLink)
		if err != nil {
			return nil, err
		}
		// example paths: /<prefix>/<version>/*
		parts := strings.Split(selfLinkURL.Path, "/")
		if len(parts) < 3 {
			return nil, fmt.Errorf("unexpected self link format: '%v'; got version '%v'", selfLink, version)
		}
		version = parts[2]
	}

	// only has list metadata
	if objectMeta == nil {
		return &corev1.ObjectReference{
			Kind:            kind,
			APIVersion:      version,
			ResourceVersion: listMeta.GetResourceVersion(),
		}, nil
	}

	return &corev1.ObjectReference{
		Kind:            kind,
		APIVersion:      version,
		Name:            objectMeta.GetName(),
		Namespace:       objectMeta.GetNamespace(),
		UID:             objectMeta.GetUID(),
		ResourceVersion: objectMeta.GetResourceVersion(),
	}, nil

}
