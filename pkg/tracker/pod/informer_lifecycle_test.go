package pod

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/werf/kubedog/pkg/dyntracker/util"
	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/tracker/internal/informertest"
	"github.com/werf/kubedog/pkg/utils"
)

const lifecycleNamespace = "default"

func TestPodsInformerRun_whenConsumerStopsReading(t *testing.T) {
	seed := func() []runtime.Object { return []runtime.Object{lifecyclePod("demo-1")} }

	informertest.RunConsumerStopsReading(t, "github.com/werf/kubedog/pkg/tracker/pod.(*PodsInformer).Run.func", seed,
		func(t *testing.T, ctx context.Context, factory *util.Concurrent[*informer.InformerFactory]) func() {
			podsInformer := NewPodsInformer(&tracker.Tracker{
				Namespace:       lifecycleNamespace,
				InformerFactory: factory,
			}, utils.ControllerAccessor(lifecycleDeployment()))
			podsInformer.PodAdded <- &corev1.Pod{}

			cleanup, err := podsInformer.Run(ctx)
			require.NoError(t, err)

			return cleanup
		})
}

func TestTrackerRunInformer_whenConsumerStopsReading(t *testing.T) {
	seed := func() []runtime.Object { return []runtime.Object{lifecyclePod("demo")} }

	informertest.RunConsumerStopsReading(t, "github.com/werf/kubedog/pkg/tracker/pod.(*Tracker).runInformer.func", seed,
		func(t *testing.T, ctx context.Context, factory *util.Concurrent[*informer.InformerFactory]) func() {
			cleanup, err := NewTracker("demo", lifecycleNamespace, nil, factory, Options{}).runInformer(ctx)
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

func lifecyclePod(name string) *corev1.Pod {
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: lifecycleNamespace,
			Labels:    map[string]string{"app": "demo"},
		},
	}
}
