package statestore

type ReadinessTaskConditionFn func(taskState *ReadinessTaskState) bool

type PresenceTaskConditionFn func(taskState *PresenceTaskState) bool

type AbsenceTaskConditionFn func(taskState *AbsenceTaskState) bool
