package dyntracker

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	watchtools "k8s.io/client-go/tools/watch"

	"github.com/werf/kubedog/pkg/display"
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
	resourceName      string
	resourceNamespace string
	resourceGVK       schema.GroupVersionKind
	resourceHumanID   string
	taskState         *util.Concurrent[*statestore.ReadinessTaskState]
	logStore          *util.Concurrent[*logstore.LogStore]
	mapper            meta.ResettableRESTMapper
	tracker           any

	timeout           time.Duration
	noActivityTimeout time.Duration

	saveLogsOnlyForContainers    []string
	saveLogsByRegex              *regexp.Regexp
	saveLogsByRegexForContainers map[string]*regexp.Regexp
	skipLogsByRegex              *regexp.Regexp
	skipLogsByRegexForContainers map[string]*regexp.Regexp
	ignoreLogs                   bool
	ignoreLogsForContainers      []string
	saveEvents                   bool
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
) (*DynamicReadinessTracker, error) {
	timeout := opts.Timeout

	var captureLogsFromTime time.Time
	if opts.CaptureLogsFromTime.IsZero() {
		captureLogsFromTime = time.Now()
	} else {
		captureLogsFromTime = opts.CaptureLogsFromTime
	}

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

	if namespaced, err := util.IsNamespaced(resourceGVK, mapper); err != nil {
		return nil, fmt.Errorf("check if namespaced: %w", err)
	} else if !namespaced {
		resourceNamespace = ""
	}

	var tracker any
	switch resourceGVK.GroupKind() {
	case schema.GroupKind{Group: "apps", Kind: "Deployment"}, schema.GroupKind{Group: "extensions", Kind: "Deployment"}:
		tracker = deployment.NewTracker(resourceName, resourceNamespace, staticClient, commontracker.Options{
			ParentContext:                            ctx,
			Timeout:                                  timeout,
			LogsFromTime:                             captureLogsFromTime,
			IgnoreReadinessProbeFailsByContainerName: ignoreReadinessProbeFailsByContainerName,
		})
	case schema.GroupKind{Group: "apps", Kind: "DaemonSet"}, schema.GroupKind{Group: "extensions", Kind: "DaemonSet"}:
		tracker = daemonset.NewTracker(resourceName, resourceNamespace, staticClient, commontracker.Options{
			ParentContext:                            ctx,
			Timeout:                                  timeout,
			LogsFromTime:                             captureLogsFromTime,
			IgnoreReadinessProbeFailsByContainerName: ignoreReadinessProbeFailsByContainerName,
		})
	case schema.GroupKind{Group: "flagger.app", Kind: "Canary"}:
		tracker = canary.NewTracker(resourceName, resourceNamespace, staticClient, dynamicClient, commontracker.Options{
			ParentContext:                            ctx,
			Timeout:                                  timeout,
			LogsFromTime:                             captureLogsFromTime,
			IgnoreReadinessProbeFailsByContainerName: ignoreReadinessProbeFailsByContainerName,
		})
	case schema.GroupKind{Group: "apps", Kind: "StatefulSet"}:
		tracker = statefulset.NewTracker(resourceName, resourceNamespace, staticClient, commontracker.Options{
			ParentContext:                            ctx,
			Timeout:                                  timeout,
			LogsFromTime:                             captureLogsFromTime,
			IgnoreReadinessProbeFailsByContainerName: ignoreReadinessProbeFailsByContainerName,
		})
	case schema.GroupKind{Group: "batch", Kind: "Job"}:
		tracker = job.NewTracker(resourceName, resourceNamespace, staticClient, commontracker.Options{
			ParentContext:                            ctx,
			Timeout:                                  timeout,
			LogsFromTime:                             captureLogsFromTime,
			IgnoreReadinessProbeFailsByContainerName: ignoreReadinessProbeFailsByContainerName,
		})
	default:
		resid := resid.NewResourceID(resourceName, resourceGVK, resid.NewResourceIDOptions{
			Namespace: resourceNamespace,
		})

		tracker = generic.NewTracker(resid, staticClient, dynamicClient, discoveryClient, mapper)
	}

	return &DynamicReadinessTracker{
		resourceName:                 resourceName,
		resourceNamespace:            resourceNamespace,
		resourceGVK:                  resourceGVK,
		resourceHumanID:              util.ResourceHumanID(resourceName, resourceNamespace, resourceGVK, mapper),
		taskState:                    taskState,
		logStore:                     logStore,
		mapper:                       mapper,
		tracker:                      tracker,
		timeout:                      timeout,
		noActivityTimeout:            noActivityTimeout,
		saveLogsOnlyForContainers:    opts.SaveLogsOnlyForContainers,
		saveLogsByRegex:              opts.SaveLogsByRegex,
		saveLogsByRegexForContainers: opts.SaveLogsByRegexForContainers,
		skipLogsByRegex:              opts.SkipLogsByRegex,
		skipLogsByRegexForContainers: opts.SkipLogsByRegexForContainers,
		ignoreLogs:                   opts.IgnoreLogs,
		ignoreLogsForContainers:      opts.IgnoreLogsForContainers,
		saveEvents:                   opts.SaveEvents,
	}, nil
}

type DynamicReadinessTrackerOptions struct {
	Timeout                                  time.Duration
	NoActivityTimeout                        time.Duration
	IgnoreReadinessProbeFailsByContainerName map[string]time.Duration
	CaptureLogsFromTime                      time.Time
	SaveLogsOnlyForContainers                []string
	SaveLogsByRegex                          *regexp.Regexp
	SaveLogsByRegexForContainers             map[string]*regexp.Regexp
	SkipLogsByRegex                          *regexp.Regexp
	SkipLogsByRegexForContainers             map[string]*regexp.Regexp
	IgnoreLogs                               bool
	IgnoreLogsForContainers                  []string
	SaveEvents                               bool
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

	trackErrCh := make(chan error, 1)
	go func() {
		trackErrCh <- tracker.Track(trackCtx)
	}()

	for {
		select {
		case err := <-trackErrCh:
			if err != nil && !errors.Is(err, commontracker.ErrStopTrack) {
				return fmt.Errorf("track resource %q: %w", t.resourceHumanID, err)
			}

			return nil
		case status := <-tracker.Added:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.handlePodsFromDeploymentStatus(&status, ts)
				t.handleDeploymentStatus(&status, ts)
				t.setRootResourceCreated(ts)
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
			status.IsReady = true

			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.handlePodsFromDeploymentStatus(&status, ts)
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
				t.handlePodsFromDeploymentStatus(&report.DeploymentStatus, ts)
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
				t.handlePodsFromDeploymentStatus(&report.DeploymentStatus, ts)
				t.handlePodsFromDeploymentPodAddedReport(&report, ts)
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
				t.handlePodsFromDeploymentStatus(&status, ts)
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
				t.handlePodsFromDeploymentStatus(&status, ts)
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
				t.handlePodsFromDeploymentStatus(&report.DeploymentStatus, ts)
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

	trackErrCh := make(chan error, 1)
	go func() {
		trackErrCh <- tracker.Track(trackCtx)
	}()

	for {
		select {
		case err := <-trackErrCh:
			if err != nil && !errors.Is(err, commontracker.ErrStopTrack) {
				return fmt.Errorf("track resource %q: %w", t.resourceHumanID, err)
			}

			return nil
		case status := <-tracker.Added:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.handlePodsFromStatefulSetStatus(&status, ts)
				t.handleStatefulSetStatus(&status, ts)
				t.setRootResourceCreated(ts)
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
			status.IsReady = true

			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.handlePodsFromStatefulSetStatus(&status, ts)
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
				t.handlePodsFromStatefulSetStatus(&report.StatefulSetStatus, ts)
				t.handlePodsFromStatefulSetPodAddedReport(&report, ts)
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
				t.handlePodsFromStatefulSetStatus(&status, ts)
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
				t.handlePodsFromStatefulSetStatus(&status, ts)
				t.handleStatefulSetStatus(&status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case logChunk := <-tracker.PodLogChunk:
			t.taskState.RTransaction(func(ts *statestore.ReadinessTaskState) {
				t.logStore.RWTransaction(func(ls *logstore.LogStore) {
					t.handlePodLogChunk(logChunk.PodLogChunk, ls, ts)
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
				t.handlePodsFromStatefulSetStatus(&report.StatefulSetStatus, ts)
				t.handleStatefulSetStatus(&report.StatefulSetStatus, ts)
				t.handlePodError(&report.ReplicaSetPodError.PodError, ts)
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

	trackErrCh := make(chan error, 1)
	go func() {
		trackErrCh <- tracker.Track(trackCtx)
	}()

	for {
		select {
		case err := <-trackErrCh:
			if err != nil && !errors.Is(err, commontracker.ErrStopTrack) {
				return fmt.Errorf("track resource %q: %w", t.resourceHumanID, err)
			}

			return nil
		case status := <-tracker.Added:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.handlePodsFromDaemonSetStatus(&status, ts)
				t.handleDaemonSetStatus(&status, ts)
				t.setRootResourceCreated(ts)
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
			status.IsReady = true

			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.handlePodsFromDaemonSetStatus(&status, ts)
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
				t.handlePodsFromDaemonSetStatus(&report.DaemonSetStatus, ts)
				t.handlePodsFromDaemonSetPodAddedReport(&report, ts)
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
				t.handlePodsFromDaemonSetStatus(&status, ts)
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
				t.handlePodsFromDaemonSetStatus(&status, ts)
				t.handleDaemonSetStatus(&status, ts)
				abort, abortErr = t.handleTaskStateStatus(ts)
			})

			if abort {
				return abortErr
			}
		case logChunk := <-tracker.PodLogChunk:
			t.taskState.RTransaction(func(ts *statestore.ReadinessTaskState) {
				t.logStore.RWTransaction(func(ls *logstore.LogStore) {
					t.handlePodLogChunk(logChunk.PodLogChunk, ls, ts)
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
				t.handlePodsFromDaemonSetStatus(&report.DaemonSetStatus, ts)
				t.handleDaemonSetStatus(&report.DaemonSetStatus, ts)
				t.handlePodError(&report.PodError.PodError, ts)
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

	trackErrCh := make(chan error, 1)
	go func() {
		trackErrCh <- tracker.Track(trackCtx)
	}()

	for {
		select {
		case err := <-trackErrCh:
			if err != nil && !errors.Is(err, commontracker.ErrStopTrack) {
				return fmt.Errorf("track resource %q: %w", t.resourceHumanID, err)
			}

			return nil
		case status := <-tracker.Added:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.handlePodsFromJobStatus(&status, ts)
				t.handleJobStatus(&status, ts)
				t.setRootResourceCreated(ts)
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
			status.IsSucceeded = true

			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.handlePodsFromJobStatus(&status, ts)
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
				t.handlePodsFromJobStatus(&report.JobStatus, ts)
				t.handlePodsFromJobPodAddedReport(&report, ts)
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
				t.handlePodsFromJobStatus(&status, ts)
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
				t.handlePodsFromJobStatus(&status, ts)
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
				t.handlePodsFromJobStatus(&report.JobStatus, ts)
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

	trackErrCh := make(chan error, 1)
	go func() {
		trackErrCh <- tracker.Track(trackCtx)
	}()

	for {
		select {
		case err := <-trackErrCh:
			if err != nil && !errors.Is(err, commontracker.ErrStopTrack) {
				return fmt.Errorf("track resource %q: %w", t.resourceHumanID, err)
			}

			return nil
		case status := <-tracker.Added:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.handleCanaryStatus(&status, ts)
				t.setRootResourceCreated(ts)
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
			status.IsSucceeded = true

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

	trackErrCh := make(chan error, 1)
	go func() {
		trackErrCh <- tracker.Track(trackCtx, t.noActivityTimeout, addedCh, succeededCh, failedCh, regularCh, eventCh)
	}()

	for {
		select {
		case err := <-trackErrCh:
			if err != nil && !errors.Is(err, commontracker.ErrStopTrack) {
				return fmt.Errorf("track resource %q: %w", t.resourceHumanID, err)
			}

			return nil
		case status := <-addedCh:
			var (
				abort    bool
				abortErr error
			)
			t.taskState.RWTransaction(func(ts *statestore.ReadinessTaskState) {
				t.handleGenericResourceStatus(status, ts)
				t.setRootResourceCreated(ts)
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

func (t *DynamicReadinessTracker) handlePodsFromDeploymentStatus(status *deployment.DeploymentStatus, taskState *statestore.ReadinessTaskState) {
	pods := lo.PickBy(status.Pods, func(_ string, podStatus pod.PodStatus) bool {
		return lo.Contains(status.NewPodsNames, podStatus.Name)
	})

	for _, pod := range pods {
		taskState.AddResourceState(pod.Name, taskState.Namespace(), podGvk)
		taskState.AddDependency(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind(), pod.Name, taskState.Namespace(), podGvk)

		if pod.StatusIndicator != nil {
			taskState.ResourceState(pod.Name, taskState.Namespace(), podGvk).RWTransaction(func(rs *statestore.ResourceState) {
				setPodStatusAttribute(rs, pod.StatusIndicator.Value)
			})
		}
	}
}

func (t *DynamicReadinessTracker) handlePodsFromStatefulSetStatus(status *statefulset.StatefulSetStatus, taskState *statestore.ReadinessTaskState) {
	pods := lo.PickBy(status.Pods, func(_ string, podStatus pod.PodStatus) bool {
		return lo.Contains(status.NewPodsNames, podStatus.Name)
	})

	for _, pod := range pods {
		taskState.AddResourceState(pod.Name, taskState.Namespace(), podGvk)
		taskState.AddDependency(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind(), pod.Name, taskState.Namespace(), podGvk)

		if pod.StatusIndicator != nil {
			taskState.ResourceState(pod.Name, taskState.Namespace(), podGvk).RWTransaction(func(rs *statestore.ResourceState) {
				setPodStatusAttribute(rs, pod.StatusIndicator.Value)
			})
		}
	}
}

func (t *DynamicReadinessTracker) handlePodsFromDaemonSetStatus(status *daemonset.DaemonSetStatus, taskState *statestore.ReadinessTaskState) {
	pods := lo.PickBy(status.Pods, func(_ string, podStatus pod.PodStatus) bool {
		return lo.Contains(status.NewPodsNames, podStatus.Name)
	})

	for _, pod := range pods {
		taskState.AddResourceState(pod.Name, taskState.Namespace(), podGvk)
		taskState.AddDependency(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind(), pod.Name, taskState.Namespace(), podGvk)

		if pod.StatusIndicator != nil {
			taskState.ResourceState(pod.Name, taskState.Namespace(), podGvk).RWTransaction(func(rs *statestore.ResourceState) {
				setPodStatusAttribute(rs, pod.StatusIndicator.Value)
			})
		}
	}
}

func (t *DynamicReadinessTracker) handlePodsFromJobStatus(status *job.JobStatus, taskState *statestore.ReadinessTaskState) {
	for _, pod := range status.Pods {
		taskState.AddResourceState(pod.Name, taskState.Namespace(), podGvk)
		taskState.AddDependency(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind(), pod.Name, taskState.Namespace(), podGvk)

		if pod.StatusIndicator != nil {
			taskState.ResourceState(pod.Name, taskState.Namespace(), podGvk).RWTransaction(func(rs *statestore.ResourceState) {
				setPodStatusAttribute(rs, pod.StatusIndicator.Value)
			})
		}
	}
}

func (t *DynamicReadinessTracker) handlePodsFromDeploymentPodAddedReport(report *deployment.PodAddedReport, taskState *statestore.ReadinessTaskState) {
	if !report.ReplicaSetPod.ReplicaSet.IsNew {
		return
	}

	taskState.AddResourceState(report.ReplicaSetPod.Name, taskState.Namespace(), podGvk)
	taskState.AddDependency(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind(), report.ReplicaSetPod.Name, taskState.Namespace(), podGvk)

	for _, pod := range report.DeploymentStatus.Pods {
		if pod.StatusIndicator != nil {
			taskState.ResourceState(report.ReplicaSetPod.Name, taskState.Namespace(), podGvk).RWTransaction(func(rs *statestore.ResourceState) {
				setPodStatusAttribute(rs, pod.StatusIndicator.Value)
			})
		}
	}
}

func (t *DynamicReadinessTracker) handlePodsFromStatefulSetPodAddedReport(report *statefulset.PodAddedReport, taskState *statestore.ReadinessTaskState) {
	taskState.AddResourceState(report.ReplicaSetPod.Name, taskState.Namespace(), podGvk)
	taskState.AddDependency(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind(), report.ReplicaSetPod.Name, taskState.Namespace(), podGvk)

	for _, pod := range report.StatefulSetStatus.Pods {
		if pod.StatusIndicator != nil {
			taskState.ResourceState(report.ReplicaSetPod.Name, taskState.Namespace(), podGvk).RWTransaction(func(rs *statestore.ResourceState) {
				setPodStatusAttribute(rs, pod.StatusIndicator.Value)
			})
		}
	}
}

func (t *DynamicReadinessTracker) handlePodsFromDaemonSetPodAddedReport(report *daemonset.PodAddedReport, taskState *statestore.ReadinessTaskState) {
	taskState.AddResourceState(report.Pod.Name, taskState.Namespace(), podGvk)
	taskState.AddDependency(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind(), report.Pod.Name, taskState.Namespace(), podGvk)

	for _, pod := range report.DaemonSetStatus.Pods {
		if pod.StatusIndicator != nil {
			taskState.ResourceState(report.Pod.Name, taskState.Namespace(), podGvk).RWTransaction(func(rs *statestore.ResourceState) {
				setPodStatusAttribute(rs, pod.StatusIndicator.Value)
			})
		}
	}
}

func (t *DynamicReadinessTracker) handlePodsFromJobPodAddedReport(report *job.PodAddedReport, taskState *statestore.ReadinessTaskState) {
	taskState.AddResourceState(report.PodName, taskState.Namespace(), podGvk)
	taskState.AddDependency(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind(), report.PodName, taskState.Namespace(), podGvk)

	for _, pod := range report.JobStatus.Pods {
		if pod.StatusIndicator != nil {
			taskState.ResourceState(report.PodName, taskState.Namespace(), podGvk).RWTransaction(func(rs *statestore.ResourceState) {
				setPodStatusAttribute(rs, pod.StatusIndicator.Value)
			})
		}
	}
}

func (t *DynamicReadinessTracker) handleDeploymentStatus(status *deployment.DeploymentStatus, taskState *statestore.ReadinessTaskState) {
	if status.ReplicasIndicator != nil {
		taskState.ResourceState(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind()).RWTransaction(func(rs *statestore.ResourceState) {
			setReplicasAttribute(rs, int(status.ReplicasIndicator.TargetValue))
		})
	}

	if status.IsReady {
		taskState.SetStatus(statestore.ReadinessTaskStatusReady)

		for _, state := range taskState.ResourceStates() {
			state.RWTransaction(func(rs *statestore.ResourceState) {
				rs.SetStatus(statestore.ResourceStatusReady)
			})
		}

		return
	}

	if status.IsFailed {
		taskState.ResourceState(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind()).RWTransaction(func(rs *statestore.ResourceState) {
			rs.AddError(errors.New(status.FailedReason), "", time.Now())
		})
	}
}

func (t *DynamicReadinessTracker) handleStatefulSetStatus(status *statefulset.StatefulSetStatus, taskState *statestore.ReadinessTaskState) {
	if status.ReplicasIndicator != nil {
		taskState.ResourceState(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind()).RWTransaction(func(rs *statestore.ResourceState) {
			setReplicasAttribute(rs, int(status.ReplicasIndicator.TargetValue))
		})
	}

	if status.IsReady {
		taskState.SetStatus(statestore.ReadinessTaskStatusReady)

		for _, state := range taskState.ResourceStates() {
			state.RWTransaction(func(rs *statestore.ResourceState) {
				rs.SetStatus(statestore.ResourceStatusReady)
			})
		}

		return
	}

	if status.IsFailed {
		taskState.ResourceState(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind()).RWTransaction(func(rs *statestore.ResourceState) {
			rs.AddError(errors.New(status.FailedReason), "", time.Now())
		})
	}
}

func (t *DynamicReadinessTracker) handleDaemonSetStatus(status *daemonset.DaemonSetStatus, taskState *statestore.ReadinessTaskState) {
	if status.ReplicasIndicator != nil {
		taskState.ResourceState(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind()).RWTransaction(func(rs *statestore.ResourceState) {
			setReplicasAttribute(rs, int(status.ReplicasIndicator.TargetValue))
		})
	}

	if status.IsReady {
		taskState.SetStatus(statestore.ReadinessTaskStatusReady)

		for _, state := range taskState.ResourceStates() {
			state.RWTransaction(func(rs *statestore.ResourceState) {
				rs.SetStatus(statestore.ResourceStatusReady)
			})
		}

		return
	}

	if status.IsFailed {
		taskState.ResourceState(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind()).RWTransaction(func(rs *statestore.ResourceState) {
			rs.AddError(errors.New(status.FailedReason), "", time.Now())
		})
	}
}

func (t *DynamicReadinessTracker) handleJobStatus(status *job.JobStatus, taskState *statestore.ReadinessTaskState) {
	taskState.ResourceState(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind()).RWTransaction(func(rs *statestore.ResourceState) {
		setReplicasAttribute(rs, 1)
	})

	if status.IsSucceeded {
		taskState.SetStatus(statestore.ReadinessTaskStatusReady)

		for _, state := range taskState.ResourceStates() {
			state.RWTransaction(func(rs *statestore.ResourceState) {
				rs.SetStatus(statestore.ResourceStatusReady)
			})
		}

		return
	}

	if status.IsFailed {
		taskState.ResourceState(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind()).RWTransaction(func(rs *statestore.ResourceState) {
			rs.AddError(errors.New(status.FailedReason), "", time.Now())
		})
	}
}

func (t *DynamicReadinessTracker) handleCanaryStatus(status *canary.CanaryStatus, taskState *statestore.ReadinessTaskState) {
	if status.IsSucceeded {
		taskState.SetStatus(statestore.ReadinessTaskStatusReady)

		for _, state := range taskState.ResourceStates() {
			state.RWTransaction(func(rs *statestore.ResourceState) {
				rs.SetStatus(statestore.ResourceStatusReady)
			})
		}

		return
	}

	if status.IsFailed {
		taskState.ResourceState(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind()).RWTransaction(func(rs *statestore.ResourceState) {
			rs.AddError(errors.New(status.FailedReason), "", time.Now())
		})
	}
}

func (t *DynamicReadinessTracker) handleGenericResourceStatus(status *generic.ResourceStatus, taskState *statestore.ReadinessTaskState) {
	if status.Indicator != nil {
		taskState.ResourceState(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind()).RWTransaction(func(rs *statestore.ResourceState) {
			setGenericConditionAttributes(rs, status)
		})
	}

	if status.IsReady() {
		taskState.SetStatus(statestore.ReadinessTaskStatusReady)

		for _, state := range taskState.ResourceStates() {
			state.RWTransaction(func(rs *statestore.ResourceState) {
				rs.SetStatus(statestore.ResourceStatusReady)
			})
		}

		return
	}

	if status.IsFailed() {
		taskState.ResourceState(taskState.Name(), taskState.Namespace(), taskState.GroupVersionKind()).RWTransaction(func(rs *statestore.ResourceState) {
			rs.AddError(errors.New(status.FailureReason()), "", time.Now())
		})

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
	if t.ignoreLogs {
		return
	}

	for _, ignoreLogForContainer := range t.ignoreLogsForContainers {
		if ignoreLogForContainer == logChunk.ContainerName {
			return
		}
	}

	if len(t.saveLogsOnlyForContainers) > 0 {
		var save bool
		for _, saveLogsOnlyForContainer := range t.saveLogsOnlyForContainers {
			if saveLogsOnlyForContainer == logChunk.ContainerName {
				save = true
				break
			}
		}
		if !save {
			return
		}
	}

	logLines := logChunk.LogLines

	if t.skipLogsByRegex != nil {
		var filteredLogLines []display.LogLine
		for _, logLine := range logLines {
			if !t.skipLogsByRegex.MatchString(logLine.Message) {
				filteredLogLines = append(filteredLogLines, logLine)
			}
		}
		logLines = filteredLogLines
	}

	if len(t.skipLogsByRegexForContainers) > 0 {
		if regex, ok := t.skipLogsByRegexForContainers[logChunk.ContainerName]; ok {
			var filteredLogLines []display.LogLine
			for _, logLine := range logLines {
				if !regex.MatchString(logLine.Message) {
					filteredLogLines = append(filteredLogLines, logLine)
				}
			}
			logLines = filteredLogLines
		}
	}

	if t.saveLogsByRegex != nil {
		var filteredLogLines []display.LogLine
		for _, logLine := range logLines {
			if t.saveLogsByRegex.MatchString(logLine.Message) {
				filteredLogLines = append(filteredLogLines, logLine)
			}
		}
		logLines = filteredLogLines
	}

	if len(t.saveLogsByRegexForContainers) > 0 {
		if regex, ok := t.saveLogsByRegexForContainers[logChunk.ContainerName]; ok {
			var filteredLogLines []display.LogLine
			for _, logLine := range logLines {
				if regex.MatchString(logLine.Message) {
					filteredLogLines = append(filteredLogLines, logLine)
				}
			}
			logLines = filteredLogLines
		}
	}

	if len(logLines) == 0 {
		return
	}

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

	for _, line := range logLines {
		resourceLogs.RWTransaction(func(rl *logstore.ResourceLogs) {
			rl.AddLogLine(line.Message, "container/"+logChunk.ContainerName, lo.Must(time.Parse(time.RFC3339, line.Timestamp)))
		})
	}
}

func (t *DynamicReadinessTracker) handleEventMessage(msg string, taskState *statestore.ReadinessTaskState, timestamp time.Time) {
	if !t.saveEvents {
		return
	}

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
		abortErr = fmt.Errorf("waiting for resource %q readiness failed", t.resourceHumanID)
		return
	default:
		panic("unexpected status")
	}

	return abort, abortErr
}

func (t *DynamicReadinessTracker) setRootResourceCreated(taskState *statestore.ReadinessTaskState) {
	rss := taskState.ResourceStates()

	if len(rss) == 0 {
		return
	}

	rss[0].RWTransaction(func(rs *statestore.ResourceState) {
		rs.SetStatus(statestore.ResourceStatusCreated)
	})
}

func setReplicasAttribute(resourceState *statestore.ResourceState, replicas int) {
	attributes := resourceState.Attributes()

	if replicasAttr, found := lo.Find(attributes, func(attr statestore.Attributer) bool {
		return attr.Name() == statestore.AttributeNameRequiredReplicas
	}); found {
		replicasAttr.(*statestore.Attribute[int]).Value = replicas
	} else {
		replicasAttr = statestore.NewAttribute(statestore.AttributeNameRequiredReplicas, replicas)
		resourceState.AddAttribute(replicasAttr)
	}
}

func setPodStatusAttribute(resourceState *statestore.ResourceState, status string) {
	attributes := resourceState.Attributes()

	if statusAttr, found := lo.Find(attributes, func(attr statestore.Attributer) bool {
		return attr.Name() == statestore.AttributeNameStatus
	}); found {
		statusAttr.(*statestore.Attribute[string]).Value = status
	} else {
		statusAttr = statestore.NewAttribute(statestore.AttributeNameStatus, status)
		resourceState.AddAttribute(statusAttr)
	}
}

func setGenericConditionAttributes(resourceState *statestore.ResourceState, resourceStatus *generic.ResourceStatus) {
	attributes := resourceState.Attributes()

	if conditionTargetAttr, found := lo.Find(attributes, func(attr statestore.Attributer) bool {
		return attr.Name() == statestore.AttributeNameConditionTarget
	}); found {
		conditionTargetAttr.(*statestore.Attribute[string]).Value = resourceStatus.HumanConditionPath()
	} else {
		conditionTargetAttr = statestore.NewAttribute(statestore.AttributeNameConditionTarget, resourceStatus.HumanConditionPath())
		resourceState.AddAttribute(conditionTargetAttr)
	}

	if resourceStatus.Indicator.Value != "" {
		if conditionCurrentValueAttr, found := lo.Find(attributes, func(attr statestore.Attributer) bool {
			return attr.Name() == statestore.AttributeNameConditionCurrentValue
		}); found {
			conditionCurrentValueAttr.(*statestore.Attribute[string]).Value = resourceStatus.Indicator.Value
		} else {
			conditionCurrentValueAttr = statestore.NewAttribute(statestore.AttributeNameConditionCurrentValue, resourceStatus.Indicator.Value)
			resourceState.AddAttribute(conditionCurrentValueAttr)
		}
	}
}

var podGvk = schema.GroupVersionKind{
	Group:   "",
	Version: "v1",
	Kind:    "Pod",
}
