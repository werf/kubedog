package job

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/tracker/internal/informertest"
	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
)

func TestRunInformer_whenConsumerStopsReading(t *testing.T) {
	seed := func() []runtime.Object {
		return []runtime.Object{&batchv1.Job{
			TypeMeta:   metav1.TypeMeta{Kind: "Job", APIVersion: "batch/v1"},
			ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default"},
		}}
	}

	informertest.RunConsumerStopsReading(t, "github.com/werf/kubedog/pkg/tracker/job.(*Tracker).runInformer.func", seed,
		func(t *testing.T, ctx context.Context, factory *util.Concurrent[*informer.InformerFactory]) func() {
			cleanup, err := NewTracker("demo", "default", nil, factory, tracker.Options{}).runInformer(ctx)
			require.NoError(t, err)

			return cleanup
		})
}
