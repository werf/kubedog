package statestore

const (
	AttributeNameRequiredReplicas      = "required replicas"
	AttributeNameStatus                = "status"
	AttributeNameConditionTarget       = "condition target"
	AttributeNameConditionCurrentValue = "condition current value"
)

type Attributer interface {
	Name() string
}

func NewAttribute[T int | string](name string, value T) *Attribute[T] {
	return &Attribute[T]{
		Value: value,
		name:  name,
	}
}

type Attribute[T int | string] struct {
	Value T

	name string
}

func (a *Attribute[T]) Name() string {
	return a.name
}
