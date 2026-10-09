package statestore

type ResourceStatus string

const (
	ResourceStatusUnknown ResourceStatus = "unknown"
	ResourceStatusReady   ResourceStatus = "ready"
	ResourceStatusCreated ResourceStatus = "created"
	ResourceStatusDeleted ResourceStatus = "deleted"
	ResourceStatusFailed  ResourceStatus = "failed"
)
