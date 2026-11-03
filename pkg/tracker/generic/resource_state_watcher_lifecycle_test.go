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

	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/tracker/internal/informertest"
	"github.com/werf/kubedog/pkg/tracker/resid"
	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
)

func TestResourceStateWatcherRun_whenConsumerStopsReading(t *testing.T) {
	gvk := corev1.SchemeGroupVersion.WithKind("ConfigMap")
	mapper := meta.NewDefaultRESTMapper(nil)
	mapper.Add(gvk, meta.RESTScopeNamespace)

	seed := func() []runtime.Object {
		return []runtime.Object{&corev1.ConfigMap{
			TypeMeta:   metav1.TypeMeta{Kind: gvk.Kind, APIVersion: gvk.GroupVersion().String()},
			ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default"},
		}}
	}

	informertest.RunConsumerStopsReading(t, "github.com/werf/kubedog/pkg/tracker/generic.(*ResourceStateWatcher).Run.func", seed,
		func(t *testing.T, ctx context.Context, factory *util.Concurrent[*informer.InformerFactory]) func() {
			watcher := NewResourceStateWatcher(resid.NewResourceID("demo", gvk, resid.NewResourceIDOptions{Namespace: "default"}), nil, mapper, factory)

			cleanup, err := watcher.Run(ctx, make(chan *unstructured.Unstructured), make(chan *unstructured.Unstructured), make(chan *unstructured.Unstructured))
			require.NoError(t, err)

			return cleanup
		})
}
