package utils

import "context"

// SendOrDone sends v to ch or gives up once ctx is done. Informer event handlers must send this way:
// a handler blocked on a channel nobody reads anymore never returns, so the shared informer can't stop
// and keeps its cache in memory.
func SendOrDone[T any](ctx context.Context, ch chan<- T, v T) {
	select {
	case ch <- v:
	case <-ctx.Done():
	}
}
