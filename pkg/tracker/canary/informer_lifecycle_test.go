package canary

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/tracker/internal/informertest"
	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
)

func TestRunInformer_whenConsumerStopsReading(t *testing.T) {
	seed := func() []runtime.Object {
		return []runtime.Object{&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "flagger.app/v1beta1",
			"kind":       "Canary",
			"metadata":   map[string]interface{}{"name": "demo", "namespace": "default"},
		}}}
	}

	informertest.RunConsumerStopsReading(t, "github.com/werf/kubedog/pkg/tracker/canary.(*Tracker).runInformer.func", seed,
		func(t *testing.T, ctx context.Context, factory *util.Concurrent[*informer.InformerFactory]) func() {
			cleanup, err := NewTracker("demo", "default", nil, nil, factory, tracker.Options{}).runInformer(ctx)
			require.NoError(t, err)

			return cleanup
		})
}
