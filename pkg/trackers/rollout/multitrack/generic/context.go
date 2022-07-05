package generic

import "context"

type Context struct {
	context    context.Context
	cancelFunc context.CancelFunc
}

func NewContext(ctx context.Context) *Context {
	if ctx == nil {
		ctx = context.Background()
	}

	newCtx, cancelFunc := context.WithCancel(ctx)

	return &Context{
		context:    newCtx,
		cancelFunc: cancelFunc,
	}
}

func (c *Context) Context() context.Context {
	return c.context
}

func (c *Context) Cancel() {
	c.cancelFunc()
}
