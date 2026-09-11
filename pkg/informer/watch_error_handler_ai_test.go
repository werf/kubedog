//go:build ai_tests

package informer

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	"github.com/werf/kubedog/pkg/display"
	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
)

var (
	eventsGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "events"}
	podsGVR   = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
)

func forbiddenErr() error {
	return apierrors.NewForbidden(schema.GroupResource{Resource: "events"}, "", errors.New("no access"))
}

type reportedNonFatalWatchError struct {
	gvr       schema.GroupVersionResource
	namespace string
	err       error
}

// newTestWatchErrorHandler wires setWatchErrorHandler to a capturing setter and returns the
// resulting handler along with the channel the fatal errors are reported to and a pointer to
// the non-fatal errors reported through the callback.
func newTestWatchErrorHandler(t *testing.T, forbiddenIsNotFatal bool) (cache.WatchErrorHandler, chan error, *[]reportedNonFatalWatchError) {
	t.Helper()

	var (
		handler   cache.WatchErrorHandler
		nonFatals []reportedNonFatalWatchError
	)
	watchErrCh := make(chan error, 10)

	err := setWatchErrorHandler(func(h cache.WatchErrorHandler) error {
		handler = h
		return nil
	}, watchErrCh, eventsGVR, informerFromFactoryOptions{
		Namespace:           metav1.NamespaceDefault,
		ForbiddenIsNotFatal: forbiddenIsNotFatal,
		OnNonFatalWatchError: func(gvr schema.GroupVersionResource, namespace string, err error) {
			nonFatals = append(nonFatals, reportedNonFatalWatchError{gvr: gvr, namespace: namespace, err: err})
		},
	})
	require.NoError(t, err)
	require.NotNil(t, handler)

	return handler, watchErrCh, &nonFatals
}

func TestSetWatchErrorHandlerForbiddenIsReportedAsNonFatal(t *testing.T) {
	handler, watchErrCh, nonFatals := newTestWatchErrorHandler(t, true)

	handler(nil, forbiddenErr())

	assert.Empty(t, watchErrCh, "forbidden error must not be reported as unrecoverable")
	require.Len(t, *nonFatals, 1)
	assert.Equal(t, eventsGVR, (*nonFatals)[0].gvr)
	assert.Equal(t, metav1.NamespaceDefault, (*nonFatals)[0].namespace)
	assert.ErrorContains(t, (*nonFatals)[0].err, "no access")
}

// The reflector retries forever, so the user must be told about the lost feed exactly once.
func TestSetWatchErrorHandlerForbiddenIsReportedOnlyOnce(t *testing.T) {
	handler, watchErrCh, nonFatals := newTestWatchErrorHandler(t, true)

	for i := 0; i < 5; i++ {
		handler(nil, forbiddenErr())
	}

	assert.Empty(t, watchErrCh)
	assert.Len(t, *nonFatals, 1)
}

func TestSetWatchErrorHandlerForbiddenFatalByDefault(t *testing.T) {
	handler, watchErrCh, nonFatals := newTestWatchErrorHandler(t, false)

	handler(nil, forbiddenErr())

	require.Len(t, watchErrCh, 1)
	assert.ErrorContains(t, <-watchErrCh, "unrecoverable watch error")
	assert.Empty(t, *nonFatals)
}

// The reflector wraps the list error, so the leniency must survive wrapping.
func TestSetWatchErrorHandlerForbiddenIsDetectedThroughWrapping(t *testing.T) {
	handler, watchErrCh, nonFatals := newTestWatchErrorHandler(t, true)

	handler(nil, fmt.Errorf("failed to list %s: %w", eventsGVR.String(), forbiddenErr()))

	assert.Empty(t, watchErrCh)
	assert.Len(t, *nonFatals, 1)
}

func TestSetWatchErrorHandlerOtherErrorsStayFatal(t *testing.T) {
	handler, watchErrCh, nonFatals := newTestWatchErrorHandler(t, true)

	handler(nil, apierrors.NewUnauthorized("token expired"))

	require.Len(t, watchErrCh, 1)
	assert.ErrorContains(t, <-watchErrCh, "unrecoverable watch error")
	assert.Empty(t, *nonFatals)
}

func TestSetWatchErrorHandlerNonFatalErrorsUnaffected(t *testing.T) {
	handler, watchErrCh, nonFatals := newTestWatchErrorHandler(t, true)

	handler(nil, io.EOF)
	handler(nil, io.ErrUnexpectedEOF)
	handler(nil, apierrors.NewResourceExpired("too old resource version"))

	assert.Empty(t, watchErrCh)
	assert.Empty(t, *nonFatals, "only the missing access is worth a warning")
}

func TestSetWatchErrorHandlerAlreadyStartedInformerIsNotAnError(t *testing.T) {
	err := setWatchErrorHandler(func(h cache.WatchErrorHandler) error {
		return errors.New("informer has already started")
	}, make(chan error, 1), eventsGVR, informerFromFactoryOptions{})

	assert.NoError(t, err)
}

func newTestInformerFactory(t *testing.T) *InformerFactory {
	t.Helper()

	var factory *InformerFactory
	NewConcurrentInformerFactory(make(chan struct{}), make(chan error, 1), dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), ConcurrentInformerFactoryOptions{
		OnNonFatalWatchError: func(_ schema.GroupVersionResource, _ string, _ error) {},
	}).RTransaction(func(f *InformerFactory) {
		factory = f
	})

	return factory
}

// Consumers disagreeing on the options must not share an informer, because a single
// informer has a single watch error handler, which can only be set once.
func TestForNamespaceSeparatesInformersByOptions(t *testing.T) {
	factory := newTestInformerFactory(t)

	strict, err := factory.ForNamespace(eventsGVR, metav1.NamespaceDefault)
	require.NoError(t, err)

	lenient, err := factory.ForNamespace(eventsGVR, metav1.NamespaceDefault, InformerOptions{ForbiddenIsNotFatal: true})
	require.NoError(t, err)

	assert.Len(t, factory.namespacedFactories, 2)
	assert.NotSame(t, sharedInformerOf(strict), sharedInformerOf(lenient))
}

func sharedInformerOf(inform *util.Concurrent[*Informer]) cache.SharedIndexInformer {
	var shared cache.SharedIndexInformer
	inform.RTransaction(func(i *Informer) {
		shared = i.informer
	})

	return shared
}

// Tracking a v1/Event itself requests the same resource and namespace both as the tracked
// resource and as the events feed, so the differing options must not break the tracking.
func TestForNamespaceSupportsTrackingEventsThemselves(t *testing.T) {
	factory := newTestInformerFactory(t)

	_, err := factory.ForNamespace(eventsGVR, "production")
	require.NoError(t, err)

	_, err = factory.ForNamespace(eventsGVR, "production", InformerOptions{ForbiddenIsNotFatal: true})
	require.NoError(t, err)
}

func TestForNamespaceReusesFactoryForSameOptions(t *testing.T) {
	factory := newTestInformerFactory(t)

	first, err := factory.ForNamespace(eventsGVR, metav1.NamespaceDefault, InformerOptions{ForbiddenIsNotFatal: true})
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		same, err := factory.ForNamespace(eventsGVR, metav1.NamespaceDefault, InformerOptions{ForbiddenIsNotFatal: true})
		require.NoError(t, err)
		assert.Same(t, sharedInformerOf(first), sharedInformerOf(same))
	}

	_, err = factory.ForNamespace(podsGVR, metav1.NamespaceDefault, InformerOptions{ForbiddenIsNotFatal: true})
	require.NoError(t, err)

	assert.Len(t, factory.namespacedFactories, 1)
}

func TestForNamespaceRejectsMoreThanOneOptions(t *testing.T) {
	factory := newTestInformerFactory(t)

	_, err := factory.ForNamespace(eventsGVR, metav1.NamespaceDefault, InformerOptions{}, InformerOptions{})

	require.ErrorContains(t, err, "at most one")
}

// newForbiddenEventsFactory returns a factory over a client denying both list and watch of
// events, along with the channel the fatal errors are reported to and the channel the
// non-fatal ones are reported to.
func newForbiddenEventsFactory(t *testing.T) (*InformerFactory, chan error, chan error) {
	t.Helper()

	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		eventsGVR: "EventList",
	})
	client.PrependReactor("list", eventsGVR.Resource, func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, forbiddenErr()
	})
	client.PrependWatchReactor(eventsGVR.Resource, func(k8stesting.Action) (bool, watch.Interface, error) {
		return true, nil, forbiddenErr()
	})

	stopCh := make(chan struct{})
	t.Cleanup(func() { close(stopCh) })

	watchErrCh := make(chan error, 10)
	nonFatalCh := make(chan error, 10)

	var factory *InformerFactory
	NewConcurrentInformerFactory(stopCh, watchErrCh, client, ConcurrentInformerFactoryOptions{
		OnNonFatalWatchError: func(_ schema.GroupVersionResource, _ string, err error) {
			nonFatalCh <- err
		},
	}).RTransaction(func(f *InformerFactory) {
		factory = f
	})

	return factory, watchErrCh, nonFatalCh
}

// awaitWatchError returns the error the reflector reported and whether it was reported as
// fatal, failing if the reflector reports nothing at all.
func awaitWatchError(t *testing.T, watchErrCh, nonFatalCh chan error) (err error, fatal bool) {
	t.Helper()

	select {
	case err := <-watchErrCh:
		return err, true
	case err := <-nonFatalCh:
		return err, false
	case <-time.After(time.Minute):
		t.Fatal("the reflector reported no error")

		return nil, false
	}
}

// The reflector is a real one here, so the option must survive the whole way from
// ForNamespace down to the handler it installs on the informer.
func TestForNamespaceLenientInformerSurvivesForbiddenEvents(t *testing.T) {
	factory, watchErrCh, nonFatalCh := newForbiddenEventsFactory(t)

	inform, err := factory.ForNamespace(eventsGVR, metav1.NamespaceDefault, InformerOptions{ForbiddenIsNotFatal: true})
	require.NoError(t, err)

	inform.RWTransaction(func(i *Informer) { i.Run() })

	reportedErr, fatal := awaitWatchError(t, watchErrCh, nonFatalCh)

	assert.False(t, fatal, "the tracking must not be stopped by the denied events")
	assert.ErrorContains(t, reportedErr, "forbidden")
}

func TestForNamespaceStrictInformerFailsOnForbiddenEvents(t *testing.T) {
	factory, watchErrCh, nonFatalCh := newForbiddenEventsFactory(t)

	inform, err := factory.ForNamespace(eventsGVR, metav1.NamespaceDefault)
	require.NoError(t, err)

	inform.RWTransaction(func(i *Informer) { i.Run() })

	reportedErr, fatal := awaitWatchError(t, watchErrCh, nonFatalCh)

	assert.True(t, fatal)
	assert.ErrorContains(t, reportedErr, "unrecoverable watch error")
}

// The warning must reach the user without the consumer opting in.
func TestNewConcurrentInformerFactoryWarnsByDefault(t *testing.T) {
	var out bytes.Buffer
	previousErr := display.Err
	t.Cleanup(func() { display.SetErr(previousErr) })
	display.SetErr(&out)

	var factory *InformerFactory
	NewConcurrentInformerFactory(make(chan struct{}), make(chan error, 1), dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), ConcurrentInformerFactoryOptions{}).
		RTransaction(func(f *InformerFactory) {
			factory = f
		})

	factory.onNonFatalWatchError(eventsGVR, metav1.NamespaceDefault, forbiddenErr())

	assert.Contains(t, out.String(), "WARNING")
	assert.Contains(t, out.String(), metav1.NamespaceDefault)
}
