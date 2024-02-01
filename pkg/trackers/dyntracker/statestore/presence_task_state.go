package statestore

import (
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
)

type PresenceTaskState struct {
	name             string
	namespace        string
	groupVersionKind schema.GroupVersionKind

	presentConditions []PresenceTaskConditionFn
	failureConditions []PresenceTaskConditionFn

	status        PresenceTaskStatus
	uuid          string
	resourceState *util.Concurrent[*ResourceState]
}

func NewPresenceTaskState(name, namespace string, groupVersionKind schema.GroupVersionKind, opts PresenceTaskStateOptions) *PresenceTaskState {
	resourceState := util.NewConcurrent(NewResourceState(name, namespace, groupVersionKind))

	presentConditions := initPresenceTaskStatePresentConditions()
	failureConditions := []PresenceTaskConditionFn{}

	uuid := uuid.NewString()

	return &PresenceTaskState{
		name:              name,
		namespace:         namespace,
		groupVersionKind:  groupVersionKind,
		presentConditions: presentConditions,
		failureConditions: failureConditions,
		uuid:              uuid,
		resourceState:     resourceState,
	}
}

type PresenceTaskStateOptions struct{}

func (s *PresenceTaskState) Name() string {
	return s.name
}

func (s *PresenceTaskState) Namespace() string {
	return s.namespace
}

func (s *PresenceTaskState) GroupVersionKind() schema.GroupVersionKind {
	return s.groupVersionKind
}

func (s *PresenceTaskState) ResourceState() *util.Concurrent[*ResourceState] {
	return s.resourceState
}

func (s *PresenceTaskState) SetStatus(status PresenceTaskStatus) {
	s.status = status
}

func (s *PresenceTaskState) Status() PresenceTaskStatus {
	if s.status != "" {
		return s.status
	}

	for _, failureCondition := range s.failureConditions {
		if failureCondition(s) {
			return PresenceTaskStatusFailed
		}
	}

	for _, presentCondition := range s.presentConditions {
		if !presentCondition(s) {
			return PresenceTaskStatusProgressing
		}
	}

	return PresenceTaskStatusPresent
}

func (s *PresenceTaskState) UUID() string {
	return s.uuid
}

func initPresenceTaskStatePresentConditions() []PresenceTaskConditionFn {
	var presentConditions []PresenceTaskConditionFn

	presentConditions = append(presentConditions, func(taskState *PresenceTaskState) bool {
		var present bool
		taskState.resourceState.RTransaction(func(rs *ResourceState) {
			switch rs.Status() {
			case ResourceStatusCreated, ResourceStatusReady:
				present = true
			}
		})

		return present
	})

	return presentConditions
}
