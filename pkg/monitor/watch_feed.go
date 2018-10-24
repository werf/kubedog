package monitor

type JobWatchFeed interface {
	Started() error
	Succeeded() error
	AddedPod(podName string) error
	LogChunk(JobLogChunk) error
	PodError(JobPodError) error
}

type LogLine struct {
	Timestamp string
	Data      string
}

type PodLogChunk struct {
	PodName       string
	ContainerName string
	LogLines      []LogLine
}

type PodError struct {
	Message       string
	PodName       string
	ContainerName string
}

type JobLogChunk struct {
	PodLogChunk
	JobName string
}

type JobPodError struct {
	PodError
	JobName string
}
