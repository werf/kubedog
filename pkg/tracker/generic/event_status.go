package generic

import corev1 "k8s.io/api/core/v1"

type EventStatus struct {
	event         *corev1.Event
	isFailure     bool
	failureReason string
}

func NewEventStatus(event *corev1.Event) *EventStatus {
	return &EventStatus{
		event: event,
	}
}

func (s *EventStatus) IsFailure() bool {
	return s.isFailure
}

func (s *EventStatus) FailureReason() string {
	return s.failureReason
}
