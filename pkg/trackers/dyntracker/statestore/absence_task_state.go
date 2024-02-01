package statestore

import (
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
)

type AbsenceTaskState struct {
	name             string
	namespace        string
	groupVersionKind schema.GroupVersionKind

	absentConditions  []AbsenceTaskConditionFn
	failureConditions []AbsenceTaskConditionFn

	status        AbsenceTaskStatus
	uuid          string
	resourceState *util.Concurrent[*ResourceState]
}

func NewAbsenceTaskState(name, namespace string, groupVersionKind schema.GroupVersionKind, opts AbsenceTaskStateOptions) *AbsenceTaskState {
	resourceState := util.NewConcurrent(NewResourceState(name, namespace, groupVersionKind))

	absentConditions := initAbsenceTaskStateAbsentConditions()
	failureConditions := []AbsenceTaskConditionFn{}

	uuid := uuid.NewString()

	return &AbsenceTaskState{
		name:              name,
		namespace:         namespace,
		groupVersionKind:  groupVersionKind,
		absentConditions:  absentConditions,
		failureConditions: failureConditions,
		uuid:              uuid,
		resourceState:     resourceState,
	}
}

type AbsenceTaskStateOptions struct{}

func (s *AbsenceTaskState) Name() string {
	return s.name
}

func (s *AbsenceTaskState) Namespace() string {
	return s.namespace
}

func (s *AbsenceTaskState) GroupVersionKind() schema.GroupVersionKind {
	return s.groupVersionKind
}

func (s *AbsenceTaskState) ResourceState() *util.Concurrent[*ResourceState] {
	return s.resourceState
}

func (s *AbsenceTaskState) SetStatus(status AbsenceTaskStatus) {
	s.status = status
}

func (s *AbsenceTaskState) Status() AbsenceTaskStatus {
	if s.status != "" {
		return s.status
	}

	for _, failureCondition := range s.failureConditions {
		if failureCondition(s) {
			return AbsenceTaskStatusFailed
		}
	}

	for _, absentCondition := range s.absentConditions {
		if !absentCondition(s) {
			return AbsenceTaskStatusProgressing
		}
	}

	return AbsenceTaskStatusAbsent
}

func (s *AbsenceTaskState) UUID() string {
	return s.uuid
}

func initAbsenceTaskStateAbsentConditions() []AbsenceTaskConditionFn {
	var absentConditions []AbsenceTaskConditionFn

	absentConditions = append(absentConditions, func(taskState *AbsenceTaskState) bool {
		var absent bool
		taskState.resourceState.RTransaction(func(rs *ResourceState) {
			if rs.Status() == ResourceStatusDeleted {
				absent = true
			}
		})

		return absent
	})

	return absentConditions
}
