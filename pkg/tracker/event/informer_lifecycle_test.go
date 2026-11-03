package event

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/werf/kubedog/pkg/dyntracker/util"
	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/tracker/internal/informertest"
)

func TestEventInformerRun_whenConsumerStopsReading(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default", UID: types.UID("pod-uid")}}

	// The factory is fresh, so its cache is still empty when Run records the initial events to
	// skip, and the seeded Event reaches the handler.
	seed := func() []runtime.Object {
		return []runtime.Object{&corev1.Event{
			TypeMeta:       metav1.TypeMeta{Kind: "Event", APIVersion: "v1"},
			ObjectMeta:     metav1.ObjectMeta{Name: "demo.1", Namespace: pod.Namespace, UID: types.UID("event-uid")},
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: pod.Name, Namespace: pod.Namespace, UID: pod.UID},
			Reason:         "Pulling",
			Message:        "Pulling image",
		}}
	}

	informertest.RunConsumerStopsReading(t, "github.com/werf/kubedog/pkg/tracker/event.(*EventInformer).Run.func", seed,
		func(t *testing.T, ctx context.Context, factory *util.Concurrent[*informer.InformerFactory]) func() {
			eventInformer := NewEventInformer(&tracker.Tracker{
				Namespace:       pod.Namespace,
				ResourceName:    pod.Name,
				InformerFactory: factory,
			}, pod).WithChannels(make(chan string), make(chan interface{}), make(chan error, 1))

			cleanup, err := eventInformer.Run(ctx)
			require.NoError(t, err)

			return cleanup
		})
}
