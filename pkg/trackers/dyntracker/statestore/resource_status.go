package statestore

type ResourceStatus string

const (
	ResourceStatusReady   ResourceStatus = "ready"
	ResourceStatusCreated ResourceStatus = "created"
	ResourceStatusDeleted ResourceStatus = "deleted"
	ResourceStatusFailed  ResourceStatus = "failed"
)
