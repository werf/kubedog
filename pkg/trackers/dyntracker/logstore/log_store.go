package logstore

import (
	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
)

type LogStore struct {
	resourcesLogs []*util.Concurrent[*ResourceLogs]
}

func NewLogStore() *LogStore {
	return &LogStore{}
}

func (s *LogStore) AddResourceLogs(resourceLogs *util.Concurrent[*ResourceLogs]) {
	s.resourcesLogs = append(s.resourcesLogs, resourceLogs)
}

func (s *LogStore) ResourcesLogs() []*util.Concurrent[*ResourceLogs] {
	return append([]*util.Concurrent[*ResourceLogs]{}, s.resourcesLogs...)
}
