package statestore

import (
	"github.com/werf/kubedog-for-werf-helm/pkg/trackers/dyntracker/util"
)

type TaskStore struct {
	readinessTasks []*util.Concurrent[*ReadinessTaskState]
	presenceTasks  []*util.Concurrent[*PresenceTaskState]
	absenceTasks   []*util.Concurrent[*AbsenceTaskState]
}

func NewTaskStore() *TaskStore {
	return &TaskStore{}
}

func (s *TaskStore) AddReadinessTaskState(task *util.Concurrent[*ReadinessTaskState]) {
	s.readinessTasks = append(s.readinessTasks, task)
}

func (s *TaskStore) AddPresenceTaskState(task *util.Concurrent[*PresenceTaskState]) {
	s.presenceTasks = append(s.presenceTasks, task)
}

func (s *TaskStore) AddAbsenceTaskState(task *util.Concurrent[*AbsenceTaskState]) {
	s.absenceTasks = append(s.absenceTasks, task)
}

func (s *TaskStore) ReadinessTasksStates() []*util.Concurrent[*ReadinessTaskState] {
	return append([]*util.Concurrent[*ReadinessTaskState]{}, s.readinessTasks...)
}

func (s *TaskStore) PresenceTasksStates() []*util.Concurrent[*PresenceTaskState] {
	return append([]*util.Concurrent[*PresenceTaskState]{}, s.presenceTasks...)
}

func (s *TaskStore) AbsenceTasksStates() []*util.Concurrent[*AbsenceTaskState] {
	return append([]*util.Concurrent[*AbsenceTaskState]{}, s.absenceTasks...)
}
