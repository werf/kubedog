package dyntracker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	watchtools "k8s.io/client-go/tools/watch"

	commontracker "github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/tracker/canary"
	"github.com/werf/kubedog/pkg/tracker/daemonset"
	"github.com/werf/kubedog/pkg/tracker/deployment"
	"github.com/werf/kubedog/pkg/tracker/generic"
	"github.com/werf/kubedog/pkg/tracker/job"
	"github.com/werf/kubedog/pkg/tracker/pod"
	"github.com/werf/kubedog/pkg/tracker/replicaset"
	"github.com/werf/kubedog/pkg/tracker/resid"
	"github.com/werf/kubedog/pkg/tracker/statefulset"
	"github.com/werf/kubedog/pkg/trackers/dyntracker/logstore"
	"github.com/werf/kubedog/pkg/trackers/dyntracker/statestore"
	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
)

type DynamicReadinessTracker struct {
	taskState *util.Concurrent[*statestore.ReadinessTaskState]
	logStore  *util.Concurrent[*logstore.LogStore]
	tracker   any

	timeout           time.Duration
	noActivityTimeout time.Duration
}

func NewDynamicReadinessTracker(
	ctx context.Context,
	taskState *util.Concurrent[*statestore.ReadinessTaskState],
	logStore *util.Concurrent[*logstore.LogStore],
	staticClient kubernetes.Interface,
	dynamicClient dynamic.Interface,
	discoveryClient discovery.CachedDiscoveryInterface,
	mapper meta.ResettableRESTMapper,
	opts DynamicReadinessTrackerOptions,
) *DynamicReadinessTracker {
	timeout := opts.Timeout
	logsFromTime := opts.LogsFromTime

	var noActivityTimeout time.Duration
	if opts.NoActivityTimeout != 0 {
		noActivityTimeout = opts.NoActivityTimeout
	} else {
		noActivityTimeout = 4 * time.Minute
	}

	ignoreReadinessProbeFailsByContainerName := make(map[string]time.Duration)
	if opts.IgnoreReadinessProbeFailsByContainerName != nil {
		ignoreReadinessProbeFailsByContainerName = opts.IgnoreReadinessProbeFailsByContainerName
	}

	var (
		resourceName      string
		resourceNamespace string
		resourceGVK       schema.GroupVersionKind
	)
	taskState.RTransaction(func(ts *statestore.ReadinessTaskState) {
		resourceName = ts.Name()
		resourceNamespace = ts.Namespace()
		resourceGVK = ts.GroupVersionKind()
	})

	var tracker any
	switch resourceGVK.GroupKind() {
	case schema.GroupKind{Group: "apps", Kind: "Deployment"}, schema.GroupKind{Group: "extensions", Kind: "Deployment"}:
		tracker = deployment.NewTracker(resourceName, resourceNamespace, staticClient, commontracker.Options{
			ParentContext:                            ctx,
			Timeout:                                  timeout,
			LogsFromTime:                             logsFromTime,
			IgnoreReadinessProbeFailsByContainerName: ignoreReadinessProbeFailsByContainerName,
		})
	case schema.GroupKind{Group: "apps", Kind: "DaemonSet"}, schema.GroupKind{Group: "extensions", Kind: "DaemonSet"}:
		tracker = daemonset.NewTracker(resourceName, resourceNamespace, staticClient, commontracker.Options{
			ParentContext:                            ctx,
			Timeout:                                  timeout,
			LogsFromTime:                             logsFromTime,
			IgnoreReadinessProbeFailsByContainerName: ignoreReadinessProbeFailsByContainerName,
		})
	case schema.GroupKind{Group: "flagger.app", Kind: "Canary"}:
		tracker = canary.NewTracker(resourceName, resourceNamespace, staticClient, commontracker.Options{
			ParentContext:                            ctx,
			Timeout:                                  timeout,
			LogsFromTime:                             logsFromTime,
			IgnoreReadinessProbeFailsByContainerName: ignoreReadinessProbeFailsByContainerName,
		})
	case schema.GroupKind{Group: "apps", Kind: "StatefulSet"}:
		tracker = statefulset.NewTracker(resourceName, resourceNamespace, staticClient, commontracker.Options{
			ParentContext:                            ctx,
			Timeout:                                  timeout,
			LogsFromTime:                             logsFromTime,
			IgnoreReadinessProbeFailsByContainerName: ignoreReadinessProbeFailsByContainerName,
		})
	case schema.GroupKind{Group: "batch", Kind: "Job"}:
		tracker = job.NewTracker(resourceName, resourceNamespace, staticClient, commontracker.Options{
			ParentContext:                            ctx,
			Timeout:                                  timeout,
			LogsFromTime:                             logsFromTime,
			IgnoreReadinessProbeFailsByContainerName: ignoreReadinessProbeFailsByContainerName,
		})
	default:
		resid := resid.NewResourceID(resourceName, resourceGVK, resid.NewResourceIDOptions{
			Namespace: resourceNamespace,
		})

		tracker = generic.NewTracker(resid, staticClient, dynamicClient, discoveryClient, mapper)
	}

	return &DynamicReadinessTracker{
		taskState:         taskState,
		logStore:          logStore,
		tracker:           tracker,
		timeout:           timeout,
		noActivityTimeout: noActivityTimeout,
	}
}

type DynamicReadinessTrackerOptions struct {
	Timeout                                  time.Duration
	NoActivityTimeout                        time.Duration
	LogsFromTime                             time.Time
	IgnoreReadinessProbeFailsByContainerName map[string]time.Duration
}

func (t *DynamicReadinessTracker) Track(ctx context.Context) error {
	switch tracker := t.tracker.(type) {
	case *deployment.Tracker:
		if err := t.trackDeployment(ctx, tracker); err != nil {
			return fmt.Errorf("track deployment: %w", err)
		}
	case *statefulset.Tracker:
		if err := t.trackStatefulSet(ctx, tracker); err != nil {
			return fmt.Errorf("track statefulset: %w", err)
		}
	case *daemonset.Tracker:
		if err := t.trackDaemonSet(ctx, tracker); err != nil {
			return fmt.Errorf("track daemonset: %w", err)
		}
	case *job.Tracker:
		if err := t.trackJob(ctx, tracker); err != nil {
			return fmt.Errorf("track job: %w", err)
		}
	case *canary.Tracker:
		if err := t.trackCanary(ctx, tracker); err != nil {
			return fmt.Errorf("track canary: %w", err)
		}
	case *generic.Tracker:
		if err := t.trackGeneric(ctx, tracker); err != nil {
			return fmt.Errorf("track generic: %w", err)
		}
	default:
		panic("unexpected tracker")
	}

	return nil
}

func (t *DynamicReadinessTracker) trackDeployment(ctx context.Context, tracker *deployment.Tracker) error {
	trackCtx, trackCtxCancelFn := watchtools.ContextWithOptionalTimeout(ctx, t.timeout)
	defer trackCtxCancelFn()

	trackErrCh := make(chan error)
	go func() {
		trackErrCh <- tracker.Track(trackCtx)
	}()

	for {
		select {
		case err := <-trackErrCh:
			if err != nil && !errors.Is(err, commontracker.ErrStopTrack) {
				return fmt.Errorf("track: %w", err)
			}

			return nil
		case status := <-tracker.Added:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromDeploymentStatus(&status, ts)
				t.handleDeploymentStatus(&status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case status := <-tracker.Ready:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromDeploymentStatus(&status, ts)
				t.handleDeploymentStatus(&status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case report := <-tracker.AddedReplicaSet:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromDeploymentStatus(&report.DeploymentStatus, ts)
				t.handleDeploymentStatus(&report.DeploymentStatus, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case report := <-tracker.AddedPod:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromDeploymentStatus(&report.DeploymentStatus, ts)
				t.addMissingPodsStatesFromDeploymentPodAddedReport(&report, ts)
				t.handleDeploymentStatus(&report.DeploymentStatus, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case status := <-tracker.Failed:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromDeploymentStatus(&status, ts)
				t.handleDeploymentStatus(&status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case status := <-tracker.Status:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromDeploymentStatus(&status, ts)
				t.handleDeploymentStatus(&status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case logChunk := <-tracker.PodLogChunk:
			t.taskState.RTransaction(func(ts *statestore.ReadinessTaskState) {
				t.logStore.RWTransaction(func(ls *logstore.LogStore) {
					t.handleReplicaSetPodLogChunk(logChunk, ls, ts)
				})
			})
		case msg := <-tracker.EventMsg:
			t.taskState.RTransaction(func(ts *statestore.ReadinessTaskState) {
				t.handleEventMessage(msg, ts, time.Now())
			})
		case report := <-tracker.PodError:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromDeploymentStatus(&report.DeploymentStatus, ts)
				t.handleDeploymentStatus(&report.DeploymentStatus, ts)
				t.handleReplicaSetPodError(&report.ReplicaSetPodError, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		}
	}
}

func (t *DynamicReadinessTracker) trackStatefulSet(ctx context.Context, tracker *statefulset.Tracker) error {
	trackCtx, trackCtxCancelFn := watchtools.ContextWithOptionalTimeout(ctx, t.timeout)
	defer trackCtxCancelFn()

	trackErrCh := make(chan error)
	go func() {
		trackErrCh <- tracker.Track(trackCtx)
	}()

	for {
		select {
		case err := <-trackErrCh:
			if err != nil && !errors.Is(err, commontracker.ErrStopTrack) {
				return fmt.Errorf("track: %w", err)
			}

			return nil
		case status := <-tracker.Added:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromStatefulSetStatus(&status, ts)
				t.handleStatefulSetStatus(&status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case status := <-tracker.Ready:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromStatefulSetStatus(&status, ts)
				t.handleStatefulSetStatus(&status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case report := <-tracker.AddedPod:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromStatefulSetStatus(&report.StatefulSetStatus, ts)
				t.addMissingPodsStatesFromStatefulSetPodAddedReport(&report, ts)
				t.handleStatefulSetStatus(&report.StatefulSetStatus, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case status := <-tracker.Failed:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromStatefulSetStatus(&status, ts)
				t.handleStatefulSetStatus(&status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case status := <-tracker.Status:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromStatefulSetStatus(&status, ts)
				t.handleStatefulSetStatus(&status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case logChunk := <-tracker.PodLogChunk:
			t.taskState.RTransaction(func(ts *statestore.ReadinessTaskState) {
				t.logStore.RWTransaction(func(ls *logstore.LogStore) {
					t.handleReplicaSetPodLogChunk(logChunk, ls, ts)
				})
			})
		case msg := <-tracker.EventMsg:
			t.taskState.RTransaction(func(ts *statestore.ReadinessTaskState) {
				t.handleEventMessage(msg, ts, time.Now())
			})
		case report := <-tracker.PodError:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromStatefulSetStatus(&report.StatefulSetStatus, ts)
				t.handleStatefulSetStatus(&report.StatefulSetStatus, ts)
				t.handleReplicaSetPodError(&report.ReplicaSetPodError, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		}
	}
}

func (t *DynamicReadinessTracker) trackDaemonSet(ctx context.Context, tracker *daemonset.Tracker) error {
	trackCtx, trackCtxCancelFn := watchtools.ContextWithOptionalTimeout(ctx, t.timeout)
	defer trackCtxCancelFn()

	trackErrCh := make(chan error)
	go func() {
		trackErrCh <- tracker.Track(trackCtx)
	}()

	for {
		select {
		case err := <-trackErrCh:
			if err != nil && !errors.Is(err, commontracker.ErrStopTrack) {
				return fmt.Errorf("track: %w", err)
			}

			return nil
		case status := <-tracker.Added:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromDaemonSetStatus(&status, ts)
				t.handleDaemonSetStatus(&status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case status := <-tracker.Ready:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromDaemonSetStatus(&status, ts)
				t.handleDaemonSetStatus(&status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case report := <-tracker.AddedPod:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromDaemonSetStatus(&report.DaemonSetStatus, ts)
				t.addMissingPodsStatesFromDaemonSetPodAddedReport(&report, ts)
				t.handleDaemonSetStatus(&report.DaemonSetStatus, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case status := <-tracker.Failed:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromDaemonSetStatus(&status, ts)
				t.handleDaemonSetStatus(&status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case status := <-tracker.Status:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromDaemonSetStatus(&status, ts)
				t.handleDaemonSetStatus(&status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case logChunk := <-tracker.PodLogChunk:
			t.taskState.RTransaction(func(ts *statestore.ReadinessTaskState) {
				t.logStore.RWTransaction(func(ls *logstore.LogStore) {
					t.handleReplicaSetPodLogChunk(logChunk, ls, ts)
				})
			})
		case msg := <-tracker.EventMsg:
			t.taskState.RTransaction(func(ts *statestore.ReadinessTaskState) {
				t.handleEventMessage(msg, ts, time.Now())
			})
		case report := <-tracker.PodError:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromDaemonSetStatus(&report.DaemonSetStatus, ts)
				t.handleDaemonSetStatus(&report.DaemonSetStatus, ts)
				t.handleReplicaSetPodError(&report.PodError, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		}
	}
}

func (t *DynamicReadinessTracker) trackJob(ctx context.Context, tracker *job.Tracker) error {
	trackCtx, trackCtxCancelFn := watchtools.ContextWithOptionalTimeout(ctx, t.timeout)
	defer trackCtxCancelFn()

	trackErrCh := make(chan error)
	go func() {
		trackErrCh <- tracker.Track(trackCtx)
	}()

	for {
		select {
		case err := <-trackErrCh:
			if err != nil && !errors.Is(err, commontracker.ErrStopTrack) {
				return fmt.Errorf("track: %w", err)
			}

			return nil
		case status := <-tracker.Added:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromJobStatus(&status, ts)
				t.handleJobStatus(&status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case status := <-tracker.Succeeded:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromJobStatus(&status, ts)
				t.handleJobStatus(&status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case report := <-tracker.AddedPod:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromJobStatus(&report.JobStatus, ts)
				t.addMissingPodsStatesFromJobPodAddedReport(&report, ts)
				t.handleJobStatus(&report.JobStatus, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case status := <-tracker.Failed:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromJobStatus(&status, ts)
				t.handleJobStatus(&status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case status := <-tracker.Status:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromJobStatus(&status, ts)
				t.handleJobStatus(&status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case logChunk := <-tracker.PodLogChunk:
			t.taskState.RTransaction(func(ts *statestore.ReadinessTaskState) {
				t.logStore.RWTransaction(func(ls *logstore.LogStore) {
					t.handlePodLogChunk(logChunk, ls, ts)
				})
			})
		case msg := <-tracker.EventMsg:
			t.taskState.RTransaction(func(ts *statestore.ReadinessTaskState) {
				t.handleEventMessage(msg, ts, time.Now())
			})
		case report := <-tracker.PodError:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.addMissingPodsStatesFromJobStatus(&report.JobStatus, ts)
				t.handleJobStatus(&report.JobStatus, ts)
				t.handlePodError(&report.PodError, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		}
	}
}

func (t *DynamicReadinessTracker) trackCanary(ctx context.Context, tracker *canary.Tracker) error {
	trackCtx, trackCtxCancelFn := watchtools.ContextWithOptionalTimeout(ctx, t.timeout)
	defer trackCtxCancelFn()

	trackErrCh := make(chan error)
	go func() {
		trackErrCh <- tracker.Track(trackCtx)
	}()

	for {
		select {
		case err := <-trackErrCh:
			if err != nil && !errors.Is(err, commontracker.ErrStopTrack) {
				return fmt.Errorf("track: %w", err)
			}

			return nil
		case status := <-tracker.Added:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.handleCanaryStatus(&status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case status := <-tracker.Succeeded:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.handleCanaryStatus(&status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case status := <-tracker.Failed:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.handleCanaryStatus(&status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case status := <-tracker.Status:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.handleCanaryStatus(&status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case msg := <-tracker.EventMsg:
			t.taskState.RTransaction(func(ts *statestore.ReadinessTaskState) {
				t.handleEventMessage(msg, ts, time.Now())
			})
		}
	}
}

func (t *DynamicReadinessTracker) trackGeneric(ctx context.Context, tracker *generic.Tracker) error {
	trackCtx, trackCtxCancelFn := watchtools.ContextWithOptionalTimeout(ctx, t.timeout)
	defer trackCtxCancelFn()

	addedCh := make(chan *generic.ResourceStatus)
	succeededCh := make(chan *generic.ResourceStatus)
	failedCh := make(chan *generic.ResourceStatus)
	regularCh := make(chan *generic.ResourceStatus, 100)
	eventCh := make(chan *corev1.Event)

	trackErrCh := make(chan error)
	go func() {
		trackErrCh <- tracker.Track(trackCtx, t.noActivityTimeout, addedCh, succeededCh, failedCh, regularCh, eventCh)
	}()

	for {
		select {
		case err := <-trackErrCh:
			if err != nil && !errors.Is(err, commontracker.ErrStopTrack) {
				return fmt.Errorf("track: %w", err)
			}

			return nil
		case status := <-addedCh:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.handleGenericResourceStatus(status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case status := <-succeededCh:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.handleGenericResourceStatus(status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case status := <-failedCh:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.handleGenericResourceStatus(status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case status := <-regularCh:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.handleGenericResourceStatus(status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case event := <-eventCh:
			t.taskState.RTransaction(func(ts *statestore.ReadinessTaskState) {
				t.handleEventMessage(event.Message, ts, event.EventTime.Time)
			})
		}
	}
}

func (t *DynamicReadinessTracker) addMissingPodsStatesFromDeploymentStatus(status *deployment.DeploymentStatus, taskState *statestore.ReadinessTaskState) {
	pods := lo.PickBy(status.Pods, func(_ string, podStatus pod.PodStatus) bool {
		return lo.Contains(status.NewPodsNames, podStatus.Name)
	})

	for _, pod := range pods {
		taskState.AddResourceState(pod.Name, taskState.Namespace(), podGvk)

		taskState.AddDependency(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind(), pod.Name, taskState.Namespace(), podGvk)
	}
}

func (t *DynamicReadinessTracker) addMissingPodsStatesFromStatefulSetStatus(status *statefulset.StatefulSetStatus, taskState *statestore.ReadinessTaskState) {
	pods := lo.PickBy(status.Pods, func(_ string, podStatus pod.PodStatus) bool {
		return lo.Contains(status.NewPodsNames, podStatus.Name)
	})

	for _, pod := range pods {
		taskState.AddResourceState(pod.Name, taskState.Namespace(), podGvk)

		taskState.AddDependency(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind(), pod.Name, taskState.Namespace(), podGvk)
	}
}

func (t *DynamicReadinessTracker) addMissingPodsStatesFromDaemonSetStatus(status *daemonset.DaemonSetStatus, taskState *statestore.ReadinessTaskState) {
	pods := lo.PickBy(status.Pods, func(_ string, podStatus pod.PodStatus) bool {
		return lo.Contains(status.NewPodsNames, podStatus.Name)
	})

	for _, pod := range pods {
		taskState.AddResourceState(pod.Name, taskState.Namespace(), podGvk)

		taskState.AddDependency(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind(), pod.Name, taskState.Namespace(), podGvk)
	}
}

func (t *DynamicReadinessTracker) addMissingPodsStatesFromJobStatus(status *job.JobStatus, taskState *statestore.ReadinessTaskState) {
	for _, pod := range status.Pods {
		taskState.AddResourceState(pod.Name, taskState.Namespace(), podGvk)

		taskState.AddDependency(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind(), pod.Name, taskState.Namespace(), podGvk)
	}
}

func (t *DynamicReadinessTracker) addMissingPodsStatesFromDeploymentPodAddedReport(report *deployment.PodAddedReport, taskState *statestore.ReadinessTaskState) {
	if !report.ReplicaSetPod.ReplicaSet.IsNew {
		return
	}

	taskState.AddResourceState(report.ReplicaSetPod.Name, taskState.Namespace(), podGvk)

	taskState.AddDependency(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind(), report.ReplicaSetPod.Name, taskState.Namespace(), podGvk)
}

func (t *DynamicReadinessTracker) addMissingPodsStatesFromStatefulSetPodAddedReport(report *statefulset.PodAddedReport, taskState *statestore.ReadinessTaskState) {
	if !report.ReplicaSetPod.ReplicaSet.IsNew {
		return
	}

	taskState.AddResourceState(report.ReplicaSetPod.Name, taskState.Namespace(), podGvk)

	taskState.AddDependency(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind(), report.ReplicaSetPod.Name, taskState.Namespace(), podGvk)
}

func (t *DynamicReadinessTracker) addMissingPodsStatesFromDaemonSetPodAddedReport(report *daemonset.PodAddedReport, taskState *statestore.ReadinessTaskState) {
	if !report.Pod.ReplicaSet.IsNew {
		return
	}

	taskState.AddResourceState(report.Pod.Name, taskState.Namespace(), podGvk)

	taskState.AddDependency(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind(), report.Pod.Name, taskState.Namespace(), podGvk)
}

func (t *DynamicReadinessTracker) addMissingPodsStatesFromJobPodAddedReport(report *job.PodAddedReport, taskState *statestore.ReadinessTaskState) {
	taskState.AddResourceState(report.PodName, taskState.Namespace(), podGvk)

	taskState.AddDependency(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind(), report.PodName, taskState.Namespace(), podGvk)
}

func (t *DynamicReadinessTracker) handleDeploymentStatus(status *deployment.DeploymentStatus, taskState *statestore.ReadinessTaskState) {
	if status.ReplicasIndicator != nil {
		replicasAttr := statestore.Attribute[int]{
			Value:    int(status.ReplicasIndicator.TargetValue),
			Internal: true,
		}

		taskState.ResourceState(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind()).RWTransaction(func(rs *statestore.ResourceState) {
			rs.SetAttribute(statestore.AttributeNameRequiredReplicas, replicasAttr)
		})
	}

	if status.IsFailed {
		taskState.ResourceState(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind()).RWTransaction(func(rs *statestore.ResourceState) {
			rs.AddError(errors.New(status.FailedReason), "", time.Now())
		})

		return
	}

	if status.IsReady {
		for _, state := range taskState.ResourceStates() {
			state.RWTransaction(func(rs *statestore.ResourceState) {
				rs.SetStatus(statestore.ResourceStatusReady)
			})
		}
	}
}

func (t *DynamicReadinessTracker) handleStatefulSetStatus(status *statefulset.StatefulSetStatus, taskState *statestore.ReadinessTaskState) {
	if status.ReplicasIndicator != nil {
		replicasAttr := statestore.Attribute[int]{
			Value:    int(status.ReplicasIndicator.TargetValue),
			Internal: true,
		}

		taskState.ResourceState(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind()).RWTransaction(func(rs *statestore.ResourceState) {
			rs.SetAttribute(statestore.AttributeNameRequiredReplicas, replicasAttr)
		})
	}

	if status.IsFailed {
		taskState.ResourceState(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind()).RWTransaction(func(rs *statestore.ResourceState) {
			rs.AddError(errors.New(status.FailedReason), "", time.Now())
		})

		return
	}

	if status.IsReady {
		for _, state := range taskState.ResourceStates() {
			state.RWTransaction(func(rs *statestore.ResourceState) {
				rs.SetStatus(statestore.ResourceStatusReady)
			})
		}
	}
}

func (t *DynamicReadinessTracker) handleDaemonSetStatus(status *daemonset.DaemonSetStatus, taskState *statestore.ReadinessTaskState) {
	if status.ReplicasIndicator != nil {
		replicasAttr := statestore.Attribute[int]{
			Value:    int(status.ReplicasIndicator.TargetValue),
			Internal: true,
		}

		taskState.ResourceState(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind()).RWTransaction(func(rs *statestore.ResourceState) {
			rs.SetAttribute(statestore.AttributeNameRequiredReplicas, replicasAttr)
		})
	}

	if status.IsFailed {
		taskState.ResourceState(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind()).RWTransaction(func(rs *statestore.ResourceState) {
			rs.AddError(errors.New(status.FailedReason), "", time.Now())
		})

		return
	}

	if status.IsReady {
		for _, state := range taskState.ResourceStates() {
			state.RWTransaction(func(rs *statestore.ResourceState) {
				rs.SetStatus(statestore.ResourceStatusReady)
			})
		}
	}
}

func (t *DynamicReadinessTracker) handleJobStatus(status *job.JobStatus, taskState *statestore.ReadinessTaskState) {
	taskState.ResourceState(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind()).RWTransaction(func(rs *statestore.ResourceState) {
		rs.SetAttribute(statestore.AttributeNameRequiredReplicas, 1)
	})

	if status.IsFailed {
		taskState.ResourceState(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind()).RWTransaction(func(rs *statestore.ResourceState) {
			rs.AddError(errors.New(status.FailedReason), "", time.Now())
		})

		return
	}

	if status.IsSucceeded {
		for _, state := range taskState.ResourceStates() {
			state.RWTransaction(func(rs *statestore.ResourceState) {
				rs.SetStatus(statestore.ResourceStatusReady)
			})
		}
	}
}

func (t *DynamicReadinessTracker) handleCanaryStatus(status *canary.CanaryStatus, taskState *statestore.ReadinessTaskState) {
	if status.IsFailed {
		taskState.ResourceState(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind()).RWTransaction(func(rs *statestore.ResourceState) {
			rs.AddError(errors.New(status.FailedReason), "", time.Now())
		})

		return
	}

	if status.IsSucceeded {
		for _, state := range taskState.ResourceStates() {
			state.RWTransaction(func(rs *statestore.ResourceState) {
				rs.SetStatus(statestore.ResourceStatusReady)
			})
		}
	}
}

func (t *DynamicReadinessTracker) handleGenericResourceStatus(status *generic.ResourceStatus, taskState *statestore.ReadinessTaskState) {
	if status.IsFailed() {
		taskState.ResourceState(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind()).RWTransaction(func(rs *statestore.ResourceState) {
			rs.AddError(errors.New(status.FailureReason()), "", time.Now())
		})

		return
	}

	if status.IsReady() {
		for _, state := range taskState.ResourceStates() {
			state.RWTransaction(func(rs *statestore.ResourceState) {
				rs.SetStatus(statestore.ResourceStatusReady)
			})
		}

		return
	}

	if status.IsDeleted() {
		for _, state := range taskState.ResourceStates() {
			state.RWTransaction(func(rs *statestore.ResourceState) {
				rs.SetStatus(statestore.ResourceStatusDeleted)
			})
		}
	}
}

func (t *DynamicReadinessTracker) handleReplicaSetPodError(podError *replicaset.ReplicaSetPodError, taskState *statestore.ReadinessTaskState) {
	if !podError.ReplicaSet.IsNew {
		return
	}

	t.handlePodError(&podError.PodError, taskState)
}

func (t *DynamicReadinessTracker) handlePodError(podError *pod.PodError, taskState *statestore.ReadinessTaskState) {
	podState := taskState.ResourceState(podError.PodName, taskState.Namespace(), podGvk)

	podState.RWTransaction(func(rs *statestore.ResourceState) {
		rs.AddError(errors.New(podError.Message), podError.ContainerName, time.Now())
	})
}

func (t *DynamicReadinessTracker) handleReplicaSetPodLogChunk(logChunk *replicaset.ReplicaSetPodLogChunk, logStore *logstore.LogStore, taskState *statestore.ReadinessTaskState) {
	if !logChunk.ReplicaSet.IsNew {
		return
	}

	t.handlePodLogChunk(logChunk.PodLogChunk, logStore, taskState)
}

func (t *DynamicReadinessTracker) handlePodLogChunk(logChunk *pod.PodLogChunk, logStore *logstore.LogStore, taskState *statestore.ReadinessTaskState) {
	namespace := taskState.Namespace()

	var resourceLogs *util.Concurrent[*logstore.ResourceLogs]
	if foundResourceLogs, found := lo.Find(logStore.ResourcesLogs(), func(crl *util.Concurrent[*logstore.ResourceLogs]) bool {
		var found bool
		crl.RTransaction(func(rl *logstore.ResourceLogs) {
			found = rl.Name() == logChunk.PodName && rl.Namespace() == namespace && rl.GroupVersionKind() == podGvk
		})

		return found
	}); found {
		resourceLogs = foundResourceLogs
	} else {
		resourceLogs = util.NewConcurrent(
			logstore.NewResourceLogs(logChunk.PodName, namespace, podGvk),
		)
		logStore.AddResourceLogs(resourceLogs)
	}

	for _, line := range logChunk.LogLines {
		resourceLogs.RWTransaction(func(rl *logstore.ResourceLogs) {
			rl.AddLogLine(line.Message, "container/"+logChunk.ContainerName, lo.Must(time.Parse(time.RFC3339, line.Timestamp)))
		})
	}
}

func (t *DynamicReadinessTracker) handleEventMessage(msg string, taskState *statestore.ReadinessTaskState, timestamp time.Time) {
	resourceState := taskState.ResourceState(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind())

	resourceState.RWTransaction(func(rs *statestore.ResourceState) {
		rs.AddEvent(msg, timestamp)
	})
}

func (t *DynamicReadinessTracker) handleTaskStateStatus(taskState *statestore.ReadinessTaskState) (abort bool, abortErr error) {
	taskStateStatus := taskState.Status()
	switch taskStateStatus {
	case statestore.ReadinessTaskStatusProgressing:
	case statestore.ReadinessTaskStatusReady:
		abort = true
		return
	case statestore.ReadinessTaskStatusFailed:
		abort = true
		abortErr = fmt.Errorf("waiting for readiness failed")
		return
	default:
		panic("unexpected status")
	}

	return abort, abortErr
}

var podGvk = schema.GroupVersionKind{
	Group:   "",
	Version: "v1",
	Kind:    "Pod",
}
