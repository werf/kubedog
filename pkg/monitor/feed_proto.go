package monitor

var (
	JobFeedStub = &JobFeedProto{
		StartedFunc:   func() error { return nil },
		SucceededFunc: func() error { return nil },
		FailedFunc:    func(string) error { return nil },
		AddedPodFunc:  func(string) error { return nil },
		LogChunkFunc:  func(JobLogChunk) error { return nil },
		PodErrorFunc:  func(JobPodError) error { return nil },
	}
)

// Prototype-struct helper to create feed with callbacks specified in-place of creation (such as JobFeedStub)
type JobFeedProto struct {
	StartedFunc   func() error
	SucceededFunc func() error
	FailedFunc    func(string) error
	AddedPodFunc  func(string) error
	LogChunkFunc  func(JobLogChunk) error
	PodErrorFunc  func(JobPodError) error
}

func (proto *JobFeedProto) Started() error {
	if proto.StartedFunc != nil {
		return proto.StartedFunc()
	}
	return nil
}
func (proto *JobFeedProto) Succeeded() error {
	if proto.SucceededFunc != nil {
		return proto.SucceededFunc()
	}
	return nil
}
func (proto *JobFeedProto) Failed(arg string) error {
	if proto.FailedFunc != nil {
		return proto.FailedFunc(arg)
	}
	return nil
}
func (proto *JobFeedProto) AddedPod(arg string) error {
	if proto.AddedPodFunc != nil {
		return proto.AddedPodFunc(arg)
	}
	return nil
}
func (proto *JobFeedProto) LogChunk(arg JobLogChunk) error {
	if proto.LogChunkFunc != nil {
		return proto.LogChunkFunc(arg)
	}
	return nil
}
func (proto *JobFeedProto) PodError(arg JobPodError) error {
	if proto.PodErrorFunc != nil {
		return proto.PodErrorFunc(arg)
	}
	return nil
}
