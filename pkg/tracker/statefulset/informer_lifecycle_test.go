package statefulset

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/werf/kubedog/pkg/dyntracker/util"
	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/tracker/internal/informertest"
)

func TestRunStatefulSetInformer_whenConsumerStopsReading(t *testing.T) {
	seed := func() []runtime.Object {
		return []runtime.Object{&appsv1.StatefulSet{
			TypeMeta:   metav1.TypeMeta{Kind: "StatefulSet", APIVersion: "apps/v1"},
			ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default"},
		}}
	}

	informertest.RunConsumerStopsReading(t, "github.com/werf/kubedog/pkg/tracker/statefulset.(*Tracker).runStatefulSetInformer.func", seed,
		func(t *testing.T, ctx context.Context, factory *util.Concurrent[*informer.InformerFactory]) func() {
			trk := NewTracker("demo", "default", nil, factory, tracker.Options{})
			trk.resourceAdded <- &appsv1.StatefulSet{}

			cleanup, err := trk.runStatefulSetInformer(ctx)
			require.NoError(t, err)

			return cleanup
		})
}
