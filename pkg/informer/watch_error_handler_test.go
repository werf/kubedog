package informer

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
)

func TestSetWatchErrorHandler_whenNobodyReadsWatchErrors(t *testing.T) {
	var handler cache.WatchErrorHandler
	watchErrCh := make(chan error, 1)
	watchErrCh <- errors.New("first watch error")
	stopCh := make(chan struct{})

	require.NoError(t, setWatchErrorHandler(func(h cache.WatchErrorHandler) error {
		handler = h
		return nil
	}, watchErrCh, stopCh, schema.GroupVersionResource{Version: "v1", Resource: "pods"}, informerFromFactoryOptions{}))

	returned := make(chan struct{})
	go func() {
		handler(nil, errors.New("connection refused"))
		close(returned)
	}()

	close(stopCh)

	select {
	case <-returned:
	case <-time.After(10 * time.Second):
		t.Fatal("watch error handler did not return after the informer was stopped")
	}
}
