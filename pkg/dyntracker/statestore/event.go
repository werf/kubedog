package statestore

import "time"

type Event struct {
	Time    time.Time
	Message string
}
