package deployment

import (
	"context"
	"fmt"

	"github.com/werf/kubedog/pkg/log"
	"github.com/werf/kubedog/pkg/tracker/pod"
	"github.com/werf/kubedog/pkg/tracker/resource"
)

// startPodsWatch starts watching the Pods of the Deployment. It does nothing if the Pods are
// already watched or if the Deployment has not been observed yet.
func (t *Tracker) startPodsWatch(ctx context.Context) error {
	if t.podsCleanupFn != nil || t.podsSelector == nil {
		return nil
	}

	cleanupFn, err := resource.Watch(ctx, t.informerFactory, podGVR, resource.WatchOptions{
		Namespace: t.namespace,
		Selector:  t.podsSelector,
	}, sendTo(ctx, t.podEvents), t.reportError)
	if err != nil {
		return fmt.Errorf("watch pods: %w", err)
	}

	t.podsCleanupFn = cleanupFn

	return nil
}

func (t *Tracker) stopPodsWatch() {
	if t.podsCleanupFn != nil {
		t.podsCleanupFn()
	}
}

// runPodTracker tracks the Pod with the Pod tracker and relays what it reports to the Tracker.
func (t *Tracker) runPodTracker(ctx context.Context, podName string) {
	ignoreLogs := t.opts.IgnoreLogs || t.savingLogsReplicas >= t.opts.SaveLogsOnlyForNumberOfReplicas
	if !ignoreLogs {
		t.savingLogsReplicas++
	}

	podCtx, cancelPodCtx := context.WithCancelCause(ctx)
	podTracker := pod.NewTracker(podName, t.namespace, t.kube, t.informerFactory, pod.Options{
		IgnoreLogs:                               ignoreLogs,
		IgnoreReadinessProbeFailsByContainerName: t.opts.IgnoreReadinessProbeFailsByContainerName,
	})
	if !t.opts.LogsFromTime.IsZero() {
		podTracker.LogsFromTime = t.opts.LogsFromTime
	}
	t.trackedPods = append(t.trackedPods, podName)

	errCh := make(chan error, 1)
	doneCh := make(chan struct{})
	go func() {
		if err := podTracker.Start(podCtx); err != nil {
			errCh <- err
		} else {
			close(doneCh)
		}
	}()

	relay := func(update podUpdate) bool {
		update.name = podName

		select {
		case t.podUpdates <- update:
			return true
		case <-ctx.Done():
			return false
		}
	}

	go func() {
		for {
			var ok bool

			select {
			case status := <-podTracker.Added:
				ok = relay(podUpdate{status: &status})
			case status := <-podTracker.Succeeded:
				ok = relay(podUpdate{status: &status})
				cancelPodCtx(fmt.Errorf("context canceled: got succeeded event for %q", podTracker.FullResourceName))
			case status := <-podTracker.Deleted:
				ok = relay(podUpdate{status: &status})
				cancelPodCtx(fmt.Errorf("context canceled: got deleted event for %q", podTracker.FullResourceName))
			case report := <-podTracker.Failed:
				ok = relay(podUpdate{status: &report.PodStatus})
			case status := <-podTracker.Ready:
				ok = relay(podUpdate{status: &status})
			case status := <-podTracker.Status:
				ok = relay(podUpdate{status: &status})
			case msg := <-podTracker.EventMsg:
				ok = relay(podUpdate{message: msg})
			case chunk := <-podTracker.ContainerLogChunk:
				ok = relay(podUpdate{logChunk: chunk})
			case report := <-podTracker.ContainerError:
				ok = relay(podUpdate{containerError: &report})
			case err := <-errCh:
				if log.Debug() {
					fmt.Printf("received pod %q error chan %v\n", podName, err)
				}

				t.reportError(err)

				return
			case <-doneCh:
				status := podTracker.LastStatus
				relay(podUpdate{done: &status})

				return
			}

			if !ok {
				return
			}
		}
	}()
}

func (t *Tracker) emit(ctx context.Context, ev Event) error {
	select {
	case t.events <- ev:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// reportError makes Track return the error. Only the first error matters, the rest are dropped.
func (t *Tracker) reportError(err error) {
	select {
	case t.errors <- err:
	default:
	}
}

// sendTo returns a watch handler that hands the events over to the channel until the context is canceled.
func sendTo[T any](ctx context.Context, ch chan<- resource.ObjectEvent[T]) func(resource.ObjectEvent[T]) {
	return func(ev resource.ObjectEvent[T]) {
		select {
		case ch <- ev:
		case <-ctx.Done():
		}
	}
}
