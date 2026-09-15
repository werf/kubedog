package logstore

import "time"

type LogLine struct {
	Time time.Time
	Line string
}
