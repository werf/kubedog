package daemonset

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/tracker/internal/informertest"
	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
)

func TestRunDaemonSetInformer_whenConsumerStopsReading(t *testing.T) {
	seed := func() []runtime.Object {
		return []runtime.Object{&appsv1.DaemonSet{
			TypeMeta:   metav1.TypeMeta{Kind: "DaemonSet", APIVersion: "apps/v1"},
			ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default"},
		}}
	}

	informertest.RunConsumerStopsReading(t, "github.com/werf/kubedog/pkg/tracker/daemonset.(*Tracker).runDaemonSetInformer.func", seed,
		func(t *testing.T, ctx context.Context, factory *util.Concurrent[*informer.InformerFactory]) func() {
			trk := NewTracker("demo", "default", nil, factory, tracker.Options{})
			trk.resourceAdded <- &appsv1.DaemonSet{}

			cleanup, err := trk.runDaemonSetInformer(ctx)
			require.NoError(t, err)

			return cleanup
		})
}
