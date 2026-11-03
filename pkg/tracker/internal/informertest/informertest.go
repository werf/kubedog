package informertest

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"

	"github.com/werf/kubedog/pkg/dyntracker/util"
	"github.com/werf/kubedog/pkg/informer"
)

const (
	sharedInformerFrame = "k8s.io/client-go/tools/cache.(*sharedIndexInformer).Run"
	listenerFrame       = "k8s.io/client-go/tools/cache.(*processorListener).run"
	processorFrame      = "k8s.io/client-go/tools/cache.(*sharedProcessor).run"
	groupWaitFrame      = "k8s.io/apimachinery/pkg/util/wait.(*Group).Wait"
)

// StartFunc starts the informer handler under test so that it blocks on a channel nobody reads.
type StartFunc func(t *testing.T, ctx context.Context, factory *util.Concurrent[*informer.InformerFactory]) (cleanup func())

type stopOrder int

const (
	cancelAndCleanup stopOrder = iota
	cancelOnly
	cleanupOnly
	stopFactoryThenCleanup
)

// RunConsumerStopsReading checks, for every way the consumer can stop, that the handler whose
// goroutine stack contains handlerFrame returns and that its shared informer stops. The informer
// lists the objects returned by seed.
func RunConsumerStopsReading(t *testing.T, handlerFrame string, seed func() []k8sruntime.Object, start StartFunc) {
	for _, tc := range []struct {
		name  string
		order stopOrder
	}{
		{name: "context canceled and cleanup", order: cancelAndCleanup},
		{name: "context canceled without cleanup", order: cancelOnly},
		{name: "cleanup with context alive", order: cleanupOnly},
		{name: "cleanup after factory stopped", order: stopFactoryThenCleanup},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Only the goroutines started by this subtest count: informers of other tests may
			// stop at any moment.
			before := goroutineIDs()
			newGoroutines := func(frames ...string) int {
				var count int
				for id := range goroutineIDs(frames...) {
					if _, ok := before[id]; !ok {
						count++
					}
				}
				return count
			}
			handlers := func() int { return newGoroutines(listenerFrame, handlerFrame) }

			stopCh := make(chan struct{})
			stopFactory := sync.OnceFunc(func() { close(stopCh) })
			t.Cleanup(stopFactory)

			dynClient := dynamicfake.NewSimpleDynamicClient(k8sscheme.Scheme, seed()...)
			factory := informer.NewConcurrentInformerFactory(stopCh, make(chan error, 10), dynClient, informer.ConcurrentInformerFactoryOptions{})

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			cleanupFn := start(t, ctx, factory)
			// A repeated RemoveEventHandler after the factory stopped with a handler still stuck
			// blocks on the lock the stopping informer holds while waiting for that handler, so
			// clean up at most once, and without waiting for a stuck first call.
			var cleanedUp atomic.Bool
			cleanup := func() {
				if cleanedUp.CompareAndSwap(false, true) {
					cleanupFn()
				}
			}
			t.Cleanup(cleanup)

			require.Eventually(t, func() bool { return handlers() > 0 }, 10*time.Second, 10*time.Millisecond,
				"handler never blocked on the channel")

			switch tc.order {
			case cancelAndCleanup:
				cancel()
				cleanup()
				stopFactory()
			case cancelOnly:
				cancel()
				// Check before the factory stops: with a handler stuck, the deferred cleanup would
				// deadlock against the stopping informer instead of letting the test fail.
				require.Eventually(t, func() bool { return handlers() == 0 }, 10*time.Second, 10*time.Millisecond,
					"handler did not return after the context was canceled")
				stopFactory()
			case cleanupOnly:
				cleanup()
				stopFactory()
			case stopFactoryThenCleanup:
				stopFactory()
				require.Eventually(t, func() bool { return newGoroutines(processorFrame, groupWaitFrame) > 0 }, 10*time.Second, 10*time.Millisecond,
					"informer never started waiting for its handlers")

				cleanedUpCh := make(chan struct{})
				go func() {
					cleanup()
					close(cleanedUpCh)
				}()
				select {
				case <-cleanedUpCh:
				case <-time.After(10 * time.Second):
					t.Fatal("cleanup did not return after the factory stopped: it must release the handler before removing it")
				}
			}

			require.Eventually(t, func() bool { return newGoroutines(sharedInformerFrame) == 0 }, 10*time.Second, 10*time.Millisecond,
				"shared informer did not stop: handler is still blocked on the channel")
		})
	}
}

// goroutineIDs returns the IDs of the goroutines whose stack contains all the frames.
func goroutineIDs(frames ...string) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, stack := range strings.Split(goroutineStacks(), "\n\n") {
		matches := true
		for _, frame := range frames {
			if !strings.Contains(stack, frame) {
				matches = false
				break
			}
		}

		// The stack starts with "goroutine <ID> [<status>]:".
		if fields := strings.Fields(stack); matches && len(fields) > 1 && fields[0] == "goroutine" {
			ids[fields[1]] = struct{}{}
		}
	}

	return ids
}

func goroutineStacks() string {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return string(buf[:n])
		}
		buf = make([]byte, 2*len(buf))
	}
}
