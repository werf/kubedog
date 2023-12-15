package statestore

const (
	AttributeNameRequiredReplicas = "RequiredReplicas"
)

type Attributer interface{}

type Attribute[T any] struct {
	Value    T
	Internal bool
}
