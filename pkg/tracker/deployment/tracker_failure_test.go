package deployment

import (
	"context"
	"errors"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/werf/kubedog/pkg/tracker/pod"
)

func TestHandleResourceFailure_whenContextCanceledWithoutReceiver(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	expectedErr := errors.New("test cancellation")

	trk := &Tracker{
		lastObject:       &appsv1.Deployment{},
		knownReplicaSets: make(map[string]*appsv1.ReplicaSet),
		podStatuses:      make(map[string]pod.PodStatus),
		rsNameByPod:      make(map[string]string),
		Failed:           make(chan DeploymentStatus),
	}

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		done <- trk.handleResourceFailure(ctx, resourceFailure{
			reason:            "failure",
			fromReplicaSetUID: types.UID("rs-uid-1"),
			mode:              FailureModeCounted,
		})
	}()

	<-started
	cancel(expectedErr)

	select {
	case err := <-done:
		if !errors.Is(err, expectedErr) {
			t.Fatalf("handleResourceFailure() error = %v, want %v", err, expectedErr)
		}
	case <-time.After(time.Second):
		t.Fatal("handleResourceFailure() remained blocked after context cancellation")
	}
}
