package monitor

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
func (proto *JobFeedProto) ContainerError(arg JobPodError) error {
	if proto.PodErrorFunc != nil {
		return proto.PodErrorFunc(arg)
	}
	return nil
}

type PodFeedProto struct {
	SucceededFunc         func() error
	FailedFunc            func() error
	ContainerLogChunkFunc func(*ContainerLogChunk) error
	PodErrorFunc          func(ContainerError) error
}

func (proto *PodFeedProto) ContainerLogChunk(arg *ContainerLogChunk) error {
	if proto.ContainerLogChunkFunc != nil {
		return proto.ContainerLogChunkFunc(arg)
	}
	return nil
}
func (proto *PodFeedProto) ContainerError(arg ContainerError) error {
	if proto.PodErrorFunc != nil {
		return proto.PodErrorFunc(arg)
	}
	return nil
}
func (proto *PodFeedProto) Succeeded() error {
	if proto.SucceededFunc != nil {
		return proto.SucceededFunc()
	}
	return nil
}
func (proto *PodFeedProto) Failed() error {
	if proto.FailedFunc != nil {
		return proto.FailedFunc()
	}
	return nil
}
