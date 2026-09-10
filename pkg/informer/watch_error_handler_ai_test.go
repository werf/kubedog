//go:build ai_tests

package informer

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/dynamicinformer"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/tools/cache"
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

	return &InformerFactory{
		dynamicClient:        dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
		namespacedFactories:  make(map[string]dynamicinformer.DynamicSharedInformerFactory),
		informerPolicies:     make(map[informerPolicyKey]InformerOptions),
		informersLock:        &sync.RWMutex{},
		stopCh:               make(chan struct{}),
		watchErrCh:           make(chan error, 1),
		onNonFatalWatchError: func(_ schema.GroupVersionResource, _ string, _ error) {},
	}
}

// A single informer has a single watch error handler, which can only be set once, so
// disagreeing consumers must be rejected instead of silently getting the policy of
// whoever created the informer first.
func TestForNamespaceRejectsConflictingOptions(t *testing.T) {
	factory := newTestInformerFactory(t)

	_, err := factory.ForNamespace(eventsGVR, metav1.NamespaceDefault, InformerOptions{ForbiddenIsNotFatal: true})
	require.NoError(t, err)

	_, err = factory.ForNamespace(eventsGVR, metav1.NamespaceDefault, InformerOptions{})
	require.ErrorContains(t, err, "conflicting options")
}

func TestForNamespaceAllowsSamePolicyAndDifferentResources(t *testing.T) {
	factory := newTestInformerFactory(t)

	for i := 0; i < 3; i++ {
		_, err := factory.ForNamespace(eventsGVR, metav1.NamespaceDefault, InformerOptions{ForbiddenIsNotFatal: true})
		require.NoError(t, err)
	}

	_, err := factory.ForNamespace(podsGVR, metav1.NamespaceDefault, InformerOptions{})
	require.NoError(t, err)

	_, err = factory.ForNamespace(eventsGVR, "production", InformerOptions{ForbiddenIsNotFatal: true})
	require.NoError(t, err)

	assert.Len(t, factory.namespacedFactories, 2, "one factory per namespace regardless of the policy")
}

func TestNewConcurrentInformerFactoryDefaultsNonFatalWatchErrorReporting(t *testing.T) {
	var factory *InformerFactory
	NewConcurrentInformerFactory(make(chan struct{}), make(chan error, 1), dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), ConcurrentInformerFactoryOptions{}).
		RTransaction(func(f *InformerFactory) {
			factory = f
		})

	assert.NotNil(t, factory.onNonFatalWatchError, "the warning must be emitted without the consumer opting in")
}
