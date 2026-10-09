package logstore

import (
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

type ResourceLogs struct {
	name             string
	namespace        string
	groupVersionKind schema.GroupVersionKind
	logs             map[string][]*LogLine
}

func NewResourceLogs(name, namespace string, groupVersionKind schema.GroupVersionKind) *ResourceLogs {
	return &ResourceLogs{
		name:             name,
		namespace:        namespace,
		groupVersionKind: groupVersionKind,
		logs:             make(map[string][]*LogLine),
	}
}

func (s *ResourceLogs) Name() string {
	return s.name
}

func (s *ResourceLogs) Namespace() string {
	return s.namespace
}

func (s *ResourceLogs) GroupVersionKind() schema.GroupVersionKind {
	return s.groupVersionKind
}

func (s *ResourceLogs) AddLogLine(line, source string, timestamp time.Time) {
	l := &LogLine{
		Time: timestamp,
		Line: line,
	}

	if _, ok := s.logs[source]; !ok {
		s.logs[source] = []*LogLine{}
	}

	s.logs[source] = append(s.logs[source], l)
}

func (s *ResourceLogs) LogLines() map[string][]*LogLine {
	result := make(map[string][]*LogLine)

	for source, logs := range s.logs {
		result[source] = append([]*LogLine{}, logs...)
	}

	return result
}
