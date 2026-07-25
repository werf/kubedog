package deployment

import (
	"context"

	"k8s.io/apimachinery/pkg/types"

	"github.com/werf/kubedog/pkg/tracker"
)

type resourceFailure struct {
	reason            string
	fromReplicaSetUID types.UID
	mode              FailureMode
}

func (d *Tracker) handleResourceFailure(ctx context.Context, failure resourceFailure) error {
	d.State = tracker.ResourceFailed
	d.failedFromReplicaSetUID = failure.fromReplicaSetUID

	status, err := d.newStatus(d.lastObject)
	if err != nil {
		return err
	}

	status.IsReady = false
	status.IsFailed = true
	status.FailedReason = failure.reason
	status.FailureMode = failure.mode

	select {
	case d.Failed <- status:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
