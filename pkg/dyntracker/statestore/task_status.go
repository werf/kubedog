package statestore

type ReadinessTaskStatus string

const (
	ReadinessTaskStatusProgressing ReadinessTaskStatus = "progressing"
	ReadinessTaskStatusReady       ReadinessTaskStatus = "ready"
	ReadinessTaskStatusFailed      ReadinessTaskStatus = "failed"
)

type PresenceTaskStatus string

const (
	PresenceTaskStatusProgressing PresenceTaskStatus = "progressing"
	PresenceTaskStatusPresent     PresenceTaskStatus = "present"
	PresenceTaskStatusFailed      PresenceTaskStatus = "failed"
)

type AbsenceTaskStatus string

const (
	AbsenceTaskStatusProgressing AbsenceTaskStatus = "progressing"
	AbsenceTaskStatusAbsent      AbsenceTaskStatus = "absent"
	AbsenceTaskStatusFailed      AbsenceTaskStatus = "failed"
)
