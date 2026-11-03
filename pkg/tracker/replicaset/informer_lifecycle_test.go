package replicaset

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
	"github.com/werf/kubedog/pkg/utils"
)

const lifecycleNamespace = "default"

func TestReplicaSetInformerRun_whenConsumerStopsReading(t *testing.T) {
	seed := func() []runtime.Object { return []runtime.Object{lifecycleReplicaSet("demo-1")} }

	informertest.RunConsumerStopsReading(t, "github.com/werf/kubedog/pkg/tracker/replicaset.(*ReplicaSetInformer).Run.func", seed,
		func(t *testing.T, ctx context.Context, factory *util.Concurrent[*informer.InformerFactory]) func() {
			rsInformer := NewReplicaSetInformer(&tracker.Tracker{
				Namespace:       lifecycleNamespace,
				InformerFactory: factory,
			}, utils.ControllerAccessor(lifecycleDeployment()))
			rsInformer.ReplicaSetAdded <- &appsv1.ReplicaSet{}

			cleanup, err := rsInformer.Run(ctx)
			require.NoError(t, err)

			return cleanup
		})
}

func lifecycleDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: lifecycleNamespace},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "demo"}},
		},
	}
}

func lifecycleReplicaSet(name string) *appsv1.ReplicaSet {
	return &appsv1.ReplicaSet{
		TypeMeta: metav1.TypeMeta{Kind: "ReplicaSet", APIVersion: "apps/v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: lifecycleNamespace,
			Labels:    map[string]string{"app": "demo"},
		},
	}
}
