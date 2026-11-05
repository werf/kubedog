package deployment

import (
	"context"
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/werf/kubedog/pkg/dyntracker/util"
	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/tracker"
	legacy "github.com/werf/kubedog/pkg/tracker/deployment"
	"github.com/werf/kubedog/pkg/tracker/pod"
	"github.com/werf/kubedog/pkg/tracker/replicaset"
)

// LegacyTracker reports what the Tracker observes through the channels of the old
// Deployment tracker, so that the Tracker can be used where the old one is. The status
// indicators the old consumers expect are built here, not in the Tracker.
type LegacyTracker struct {
	legacy.Feed

	tracker *Tracker
}

func NewLegacyTracker(name, namespace string, kube kubernetes.Interface, informerFactory *util.Concurrent[*informer.InformerFactory], opts tracker.Options) *LegacyTracker {
	return &LegacyTracker{
		Feed: legacy.NewFeed(),
		tracker: NewTracker(name, namespace, kube, informerFactory, Options{
			LogsFromTime:                             opts.LogsFromTime,
			SaveLogsOnlyForNumberOfReplicas:          opts.SaveLogsOnlyForNumberOfReplicas,
			IgnoreLogs:                               opts.IgnoreLogs,
			IgnoreReadinessProbeFailsByContainerName: opts.IgnoreReadinessProbeFailsByContainerName,
		}),
	}
}

func (l *LegacyTracker) Track(ctx context.Context) error {
	trackErrCh := make(chan error, 1)
	go func() {
		trackErrCh <- l.tracker.Track(ctx)
	}()

	for {
		select {
		case err := <-trackErrCh:
			return err
		case ev := <-l.tracker.Events():
			if err := l.forward(ctx, ev); err != nil {
				return err
			}
		}
	}
}

func (l *LegacyTracker) forward(ctx context.Context, ev Event) error {
	switch ev := ev.(type) {
	case AddedEvent:
		return send(ctx, l.Added, legacyStatus(ev.Status))
	case ReadyEvent:
		return send(ctx, l.Ready, legacyStatus(ev.Status))
	case FailedEvent:
		return send(ctx, l.Failed, legacyStatus(ev.Status))
	case StatusEvent:
		return send(ctx, l.Status, legacyStatus(ev.Status))
	case DeletedEvent:
		return send(ctx, l.Status, legacy.DeploymentStatus{})
	case ReplicaSetAddedEvent:
		return send(ctx, l.AddedReplicaSet, legacy.ReplicaSetAddedReport{
			ReplicaSet:       legacyReplicaSet(ev.ReplicaSet),
			DeploymentStatus: legacyStatus(ev.Status),
		})
	case PodAddedEvent:
		return send(ctx, l.AddedPod, legacy.PodAddedReport{
			ReplicaSetPod: replicaset.ReplicaSetPod{
				Name:       ev.PodName,
				ReplicaSet: legacyReplicaSet(ev.ReplicaSet),
			},
			DeploymentStatus: legacyStatus(ev.Status),
		})
	case PodLogChunkEvent:
		return send(ctx, l.PodLogChunk, &replicaset.ReplicaSetPodLogChunk{
			PodLogChunk: &pod.PodLogChunk{
				ContainerLogChunk: ev.Chunk,
				PodName:           ev.PodName,
			},
			ReplicaSet: legacyReplicaSet(ev.ReplicaSet),
		})
	case PodErrorEvent:
		return send(ctx, l.PodError, legacy.PodErrorReport{
			ReplicaSetPodError: replicaset.ReplicaSetPodError{
				PodError: pod.PodError{
					ContainerError: ev.ContainerError,
					PodName:        ev.PodName,
				},
				ReplicaSet: legacyReplicaSet(ev.ReplicaSet),
			},
			DeploymentStatus: legacyStatus(ev.Status),
		})
	case MessageEvent:
		return send(ctx, l.EventMsg, ev.Message)
	default:
		return fmt.Errorf("unexpected event type %T", ev)
	}
}

func legacyStatus(status Status) legacy.DeploymentStatus {
	if status.Object == nil {
		return legacy.DeploymentStatus{
			StatusGeneration: status.Generation,
			IsFailed:         status.Failed,
			FailedReason:     status.FailedReason,
		}
	}

	return legacy.NewDeploymentStatus(status.Object, status.Generation, status.Failed, status.FailedReason, status.Pods, status.NewPodsNames)
}

func legacyReplicaSet(rs ReplicaSet) replicaset.ReplicaSet {
	return replicaset.ReplicaSet{Name: rs.Name, IsNew: rs.IsNew}
}

func send[T any](ctx context.Context, ch chan<- T, value T) error {
	select {
	case ch <- value:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
