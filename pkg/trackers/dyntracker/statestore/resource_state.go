package statestore

import (
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
)

type ResourceState struct {
	name             string
	namespace        string
	groupVersionKind schema.GroupVersionKind

	status     ResourceStatus
	attributes []Attributer
	events     []*Event
	errors     map[string][]*Error
}

func NewResourceState(name, namespace string, groupVersionKind schema.GroupVersionKind) *ResourceState {
	return &ResourceState{
		name:             name,
		namespace:        namespace,
		groupVersionKind: groupVersionKind,
		status:           ResourceStatusUnknown,
		errors:           make(map[string][]*Error),
	}
}

func (s *ResourceState) Name() string {
	return s.name
}

func (s *ResourceState) Namespace() string {
	return s.namespace
}

func (s *ResourceState) GroupVersionKind() schema.GroupVersionKind {
	return s.groupVersionKind
}

func (s *ResourceState) SetStatus(status ResourceStatus) {
	s.status = status
}

func (s *ResourceState) Status() ResourceStatus {
	return s.status
}

func (s *ResourceState) AddError(err error, source string, timestamp time.Time) {
	e := &Error{
		Time: timestamp,
		Err:  err,
	}

	if _, ok := s.errors[source]; !ok {
		s.errors[source] = []*Error{}
	}

	s.errors[source] = append(s.errors[source], e)
}

func (s *ResourceState) Errors() map[string][]*Error {
	result := make(map[string][]*Error)

	for source, errors := range s.errors {
		result[source] = append([]*Error{}, errors...)
	}

	return result
}

func (s *ResourceState) AddEvent(message string, timestamp time.Time) {
	e := &Event{
		Time:    timestamp,
		Message: message,
	}

	s.events = append(s.events, e)
}

func (s *ResourceState) Events() []*Event {
	return append([]*Event{}, s.events...)
}

func (s *ResourceState) AddAttribute(attr Attributer) {
	s.attributes = append(s.attributes, attr)
}

func (s *ResourceState) Attributes() []Attributer {
	return append([]Attributer{}, s.attributes...)
}

func (s *ResourceState) ID() string {
	return util.ResourceID(s.name, s.namespace, s.groupVersionKind)
}
