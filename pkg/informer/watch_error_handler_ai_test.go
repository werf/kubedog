//go:build ai_tests

package informer

import (
	"errors"
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

var eventsGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "events"}

func forbiddenErr() error {
	return apierrors.NewForbidden(schema.GroupResource{Resource: "events"}, "", errors.New("no access"))
}

// newTestWatchErrorHandler wires setWatchErrorHandler to a capturing setter and returns
// the resulting handler along with the channel the fatal errors are reported to.
func newTestWatchErrorHandler(t *testing.T, forbiddenIsNotFatal bool) (cache.WatchErrorHandler, chan error) {
	t.Helper()

	var handler cache.WatchErrorHandler
	watchErrCh := make(chan error, 10)

	err := setWatchErrorHandler(func(h cache.WatchErrorHandler) error {
		handler = h
		return nil
	}, watchErrCh, eventsGVR, forbiddenIsNotFatal)
	require.NoError(t, err)
	require.NotNil(t, handler)

	return handler, watchErrCh
}

func TestSetWatchErrorHandlerForbiddenNotFatal(t *testing.T) {
	handler, watchErrCh := newTestWatchErrorHandler(t, true)

	handler(nil, forbiddenErr())

	assert.Empty(t, watchErrCh, "forbidden error must not be reported as unrecoverable")
}

func TestSetWatchErrorHandlerForbiddenFatalByDefault(t *testing.T) {
	handler, watchErrCh := newTestWatchErrorHandler(t, false)

	handler(nil, forbiddenErr())

	require.Len(t, watchErrCh, 1)
	assert.ErrorContains(t, <-watchErrCh, "unrecoverable watch error")
}

func TestSetWatchErrorHandlerOtherErrorsStayFatal(t *testing.T) {
	handler, watchErrCh := newTestWatchErrorHandler(t, true)

	handler(nil, apierrors.NewUnauthorized("token expired"))

	require.Len(t, watchErrCh, 1)
	assert.ErrorContains(t, <-watchErrCh, "unrecoverable watch error")
}

func TestSetWatchErrorHandlerNonFatalErrorsUnaffected(t *testing.T) {
	handler, watchErrCh := newTestWatchErrorHandler(t, true)

	handler(nil, io.EOF)
	handler(nil, io.ErrUnexpectedEOF)
	handler(nil, apierrors.NewResourceExpired("too old resource version"))

	assert.Empty(t, watchErrCh)
}

func TestSetWatchErrorHandlerAlreadyStartedInformerIsNotAnError(t *testing.T) {
	err := setWatchErrorHandler(func(h cache.WatchErrorHandler) error {
		return errors.New("informer has already started")
	}, make(chan error, 1), eventsGVR, false)

	assert.NoError(t, err)
}

func newTestInformerFactory(t *testing.T) *InformerFactory {
	t.Helper()

	return &InformerFactory{
		dynamicClient:       dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
		namespacedFactories: make(map[string]dynamicinformer.DynamicSharedInformerFactory),
		informersLock:       &sync.RWMutex{},
		stopCh:              make(chan struct{}),
		watchErrCh:          make(chan error, 1),
	}
}

// The watch error policy must be a part of the informer identity: a single informer has a
// single watch error handler, so the consumers disagreeing on the policy can't share one.
func TestForNamespaceSeparatesInformersByPolicy(t *testing.T) {
	factory := newTestInformerFactory(t)

	_, err := factory.ForNamespace(eventsGVR, metav1.NamespaceDefault)
	require.NoError(t, err)

	_, err = factory.ForNamespace(eventsGVR, metav1.NamespaceDefault, InformerOptions{ForbiddenIsNotFatal: true})
	require.NoError(t, err)

	require.Len(t, factory.namespacedFactories, 2)
	assert.Contains(t, factory.namespacedFactories, metav1.NamespaceDefault)
	assert.Contains(t, factory.namespacedFactories, metav1.NamespaceDefault+"/forbidden-is-not-fatal")
}

func TestForNamespaceReusesInformerForSamePolicy(t *testing.T) {
	factory := newTestInformerFactory(t)

	for i := 0; i < 3; i++ {
		_, err := factory.ForNamespace(eventsGVR, metav1.NamespaceDefault, InformerOptions{ForbiddenIsNotFatal: true})
		require.NoError(t, err)
	}

	_, err := factory.ForNamespace(eventsGVR, "production", InformerOptions{ForbiddenIsNotFatal: true})
	require.NoError(t, err)

	require.Len(t, factory.namespacedFactories, 2)
}

func TestNamespacedFactoryKeyNeverCollidesWithNamespace(t *testing.T) {
	// A namespace name can't contain a slash, so the lenient key is unreachable by any
	// namespace passed in as is.
	assert.Equal(t, "prod", namespacedFactoryKey("prod", InformerOptions{}))
	assert.Equal(t, "prod/forbidden-is-not-fatal", namespacedFactoryKey("prod", InformerOptions{ForbiddenIsNotFatal: true}))
	assert.NotEqual(t,
		namespacedFactoryKey("prod", InformerOptions{}),
		namespacedFactoryKey("prod", InformerOptions{ForbiddenIsNotFatal: true}),
	)
}
