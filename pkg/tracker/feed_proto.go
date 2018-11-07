package tracker

type JobFeedProto struct {
	AddedFunc       func() error
	SucceededFunc   func() error
	FailedFunc      func(string) error
	AddedPodFunc    func(string) error
	PodLogChunkFunc func(*PodLogChunk) error
	PodErrorFunc    func(PodError) error
}

func (proto *JobFeedProto) Added() error {
	if proto.AddedFunc != nil {
		return proto.AddedFunc()
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
func (proto *JobFeedProto) PodLogChunk(arg *PodLogChunk) error {
	if proto.PodLogChunkFunc != nil {
		return proto.PodLogChunkFunc(arg)
	}
	return nil
}
func (proto *JobFeedProto) PodError(arg PodError) error {
	if proto.PodErrorFunc != nil {
		return proto.PodErrorFunc(arg)
	}
	return nil
}

type PodFeedProto struct {
	AddedFunc             func() error
	SucceededFunc         func() error
	FailedFunc            func() error
	ContainerLogChunkFunc func(*ContainerLogChunk) error
	ContainerErrorFunc    func(ContainerError) error
}

func (proto *PodFeedProto) ContainerLogChunk(arg *ContainerLogChunk) error {
	if proto.ContainerLogChunkFunc != nil {
		return proto.ContainerLogChunkFunc(arg)
	}
	return nil
}
func (proto *PodFeedProto) ContainerError(arg ContainerError) error {
	if proto.ContainerErrorFunc != nil {
		return proto.ContainerErrorFunc(arg)
	}
	return nil
}
func (proto *PodFeedProto) Added() error {
	if proto.AddedFunc != nil {
		return proto.AddedFunc()
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

type DeploymentFeedProto struct {
	AddedFunc           func(bool) error
	CompletedFunc       func() error
	FailedFunc          func(string) error
	AddedReplicaSetFunc func(string) error
	AddedPodFunc        func(string, string, bool) error
	PodLogChunkFunc     func(*PodLogChunk) error
	PodErrorFunc        func(PodError) error
}

func (proto *DeploymentFeedProto) Added(completed bool) error {
	if proto.AddedFunc != nil {
		return proto.AddedFunc(completed)
	}
	return nil
}
func (proto *DeploymentFeedProto) Completed() error {
	if proto.CompletedFunc != nil {
		return proto.CompletedFunc()
	}
	return nil
}
func (proto *DeploymentFeedProto) Failed(arg string) error {
	if proto.FailedFunc != nil {
		return proto.FailedFunc(arg)
	}
	return nil
}
func (proto *DeploymentFeedProto) AddedReplicaSet(arg string) error {
	if proto.AddedReplicaSetFunc != nil {
		return proto.AddedReplicaSetFunc(arg)
	}
	return nil
}
func (proto *DeploymentFeedProto) AddedPod(arg1 string, arg2 string, arg3 bool) error {
	if proto.AddedPodFunc != nil {
		return proto.AddedPodFunc(arg1, arg2, arg3)
	}
	return nil
}
func (proto *DeploymentFeedProto) PodLogChunk(arg *PodLogChunk) error {
	if proto.PodLogChunkFunc != nil {
		return proto.PodLogChunkFunc(arg)
	}
	return nil
}
func (proto *DeploymentFeedProto) PodError(arg PodError) error {
	if proto.PodErrorFunc != nil {
		return proto.PodErrorFunc(arg)
	}
	return nil
}
