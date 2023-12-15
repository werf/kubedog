package statestore

import "time"

type Error struct {
	Time time.Time
	Err  error
}
