package generic

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/tracker/internal/informertest"
	"github.com/werf/kubedog/pkg/tracker/resid"
	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
)

func TestResourceEventsWatcherRun_whenConsumerStopsReading(t *testing.T) {
	gvk := corev1.SchemeGroupVersion.WithKind("ConfigMap")
	mapper := meta.NewDefaultRESTMapper(nil)
	mapper.Add(gvk, meta.RESTScopeNamespace)

	object := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": gvk.GroupVersion().String(),
		"kind":       gvk.Kind,
		"metadata":   map[string]interface{}{"name": "demo", "namespace": "default", "uid": "configmap-uid"},
	}}

	// The factory is fresh, so its cache is still empty when Run records the initial events to
	// skip, and the seeded Event reaches the handler.
	seed := func() []runtime.Object {
		return []runtime.Object{&corev1.Event{
			TypeMeta:       metav1.TypeMeta{Kind: "Event", APIVersion: "v1"},
			ObjectMeta:     metav1.ObjectMeta{Name: "demo.1", Namespace: "default", UID: types.UID("event-uid")},
			InvolvedObject: corev1.ObjectReference{Kind: gvk.Kind, Name: "demo", Namespace: "default", UID: object.GetUID()},
			Reason:         "Updated",
			Message:        "Updated",
		}}
	}

	informertest.RunConsumerStopsReading(t, "github.com/werf/kubedog/pkg/tracker/generic.(*ResourceEventsWatcher).Run.func", seed,
		func(t *testing.T, ctx context.Context, factory *util.Concurrent[*informer.InformerFactory]) func() {
			watcher := NewResourceEventsWatcher(object, resid.NewResourceID("demo", gvk, resid.NewResourceIDOptions{Namespace: "default"}), mapper, factory)

			cleanup, err := watcher.Run(ctx, make(chan *corev1.Event))
			require.NoError(t, err)

			return cleanup
		})
}
