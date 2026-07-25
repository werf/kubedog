package deployment

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/werf/kubedog/pkg/dyntracker/util"
	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/log"
	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/tracker/event"
	"github.com/werf/kubedog/pkg/tracker/pod"
	"github.com/werf/kubedog/pkg/tracker/replicaset"
	"github.com/werf/kubedog/pkg/utils"
)

// replicaSetFailedCreateReason is set by the ReplicaSet controller on the
// ReplicaSetReplicaFailure condition when it cannot create pods.
const replicaSetFailedCreateReason = "FailedCreate"

type ReplicaSetAddedReport struct {
	ReplicaSet       replicaset.ReplicaSet
	DeploymentStatus DeploymentStatus
}

type PodAddedReport struct {
	ReplicaSetPod    replicaset.ReplicaSetPod
	DeploymentStatus DeploymentStatus
}

type PodErrorReport struct {
	ReplicaSetPodError replicaset.ReplicaSetPodError
	DeploymentStatus   DeploymentStatus
}

type Tracker struct {
	tracker.Tracker

	State             tracker.TrackerState
	Conditions        []string
	NewReplicaSetName string

	knownReplicaSets           map[string]*appsv1.ReplicaSet
	deletedReplicaSetUIDs      map[types.UID]struct{}
	lastObject                 *appsv1.Deployment
	failedFromReplicaSetUID    types.UID
	reportedReplicaSetFailures map[types.UID]struct{}
	replicaSetsSynced          bool
	podStatuses                map[string]pod.PodStatus
	rsNameByPod                map[string]string

	ignoreLogs                               bool
	ignoreReadinessProbeFailsByContainerName map[string]time.Duration
	savingLogsReplicas                       int

	TrackedPodsNames []string

	Added  chan DeploymentStatus
	Ready  chan DeploymentStatus
	Failed chan DeploymentStatus
	Status chan DeploymentStatus

	EventMsg        chan string
	AddedReplicaSet chan ReplicaSetAddedReport
	AddedPod        chan PodAddedReport
	PodLogChunk     chan *replicaset.ReplicaSetPodLogChunk
	PodError        chan PodErrorReport

	resourceAdded        chan *appsv1.Deployment
	resourceModified     chan *appsv1.Deployment
	resourceDeleted      chan *appsv1.Deployment
	resourceFailed       chan interface{}
	replicaSetAdded      chan *appsv1.ReplicaSet
	replicaSetModified   chan *appsv1.ReplicaSet
	replicaSetDeleted    chan *appsv1.ReplicaSet
	replicaSetUnselected chan *appsv1.ReplicaSet
	replicaSetSynced     chan struct{}
	errors               chan error

	podAddedRelay           chan *corev1.Pod
	podStatusesRelay        chan map[string]pod.PodStatus
	podLogChunksRelay       chan map[string]*pod.ContainerLogChunk
	podContainerErrorsRelay chan map[string]pod.ContainerErrorReport
	donePodsRelay           chan map[string]pod.PodStatus
}

func NewTracker(name, namespace string, kube kubernetes.Interface, informerFactory *util.Concurrent[*informer.InformerFactory], opts tracker.Options) *Tracker {
	return &Tracker{
		Tracker: tracker.Tracker{
			Kube:                            kube,
			Namespace:                       namespace,
			FullResourceName:                fmt.Sprintf("deploy/%s", name),
			ResourceName:                    name,
			SaveLogsOnlyForNumberOfReplicas: opts.SaveLogsOnlyForNumberOfReplicas,
			LogsFromTime:                    opts.LogsFromTime,
			InformerFactory:                 informerFactory,
		},

		Added:  make(chan DeploymentStatus, 1),
		Ready:  make(chan DeploymentStatus),
		Failed: make(chan DeploymentStatus),
		Status: make(chan DeploymentStatus, 100),

		EventMsg:        make(chan string, 1),
		AddedReplicaSet: make(chan ReplicaSetAddedReport, 10),
		AddedPod:        make(chan PodAddedReport, 10),
		PodLogChunk:     make(chan *replicaset.ReplicaSetPodLogChunk, 1000),
		PodError:        make(chan PodErrorReport),

		knownReplicaSets:           make(map[string]*appsv1.ReplicaSet),
		deletedReplicaSetUIDs:      make(map[types.UID]struct{}),
		reportedReplicaSetFailures: make(map[types.UID]struct{}),
		podStatuses:                make(map[string]pod.PodStatus),
		rsNameByPod:                make(map[string]string),

		ignoreLogs:                               opts.IgnoreLogs,
		ignoreReadinessProbeFailsByContainerName: opts.IgnoreReadinessProbeFailsByContainerName,

		errors:           make(chan error, 1),
		resourceAdded:    make(chan *appsv1.Deployment, 1),
		resourceModified: make(chan *appsv1.Deployment, 1),
		resourceDeleted:  make(chan *appsv1.Deployment, 1),
		resourceFailed:   make(chan interface{}, 1),
		// ReplicaSet events come from a single informer goroutine and fan out into three
		// channels. Unbuffered sends keep them in order: a buffered Added could otherwise
		// be consumed after a newer Modified and overwrite a fresh snapshot with a stale one.
		replicaSetAdded:      make(chan *appsv1.ReplicaSet),
		replicaSetModified:   make(chan *appsv1.ReplicaSet),
		replicaSetDeleted:    make(chan *appsv1.ReplicaSet),
		replicaSetUnselected: make(chan *appsv1.ReplicaSet),
		replicaSetSynced:     make(chan struct{}),

		podAddedRelay:           make(chan *corev1.Pod, 1),
		podStatusesRelay:        make(chan map[string]pod.PodStatus, 10),
		podLogChunksRelay:       make(chan map[string]*pod.ContainerLogChunk, 10),
		podContainerErrorsRelay: make(chan map[string]pod.ContainerErrorReport, 10),
		donePodsRelay:           make(chan map[string]pod.PodStatus, 10),
	}
}

// Track starts tracking of deployment rollout process.
// watch only for one deployment resource with name d.ResourceName within the namespace with name d.Namespace
// Watcher can wait for namespace creation and then for deployment creation
// watcher receives added event if deployment is started
// watch is infinite by default
// there is option StopOnAvailable — if true, watcher stops after deployment has available status
// you can define custom stop triggers using custom implementation of ControllerFeed.
func (d *Tracker) Track(ctx context.Context) (err error) {
	deploymentInformerCleanupFn, err := d.runDeploymentInformer(ctx)
	if err != nil {
		return err
	}
	defer deploymentInformerCleanupFn()

	for {
		select {
		case object := <-d.resourceAdded:
			cleanupFn, err := d.handleDeploymentState(ctx, object)
			if err != nil {
				return err
			}
			defer cleanupFn()
			if err := d.handleNewReplicaSetFailure(ctx); err != nil {
				return err
			}
		case object := <-d.resourceModified:
			cleanupFn, err := d.handleDeploymentState(ctx, object)
			if err != nil {
				return err
			}
			defer cleanupFn()
			if err := d.handleNewReplicaSetFailure(ctx); err != nil {
				return err
			}
		case <-d.resourceDeleted:
			d.State = tracker.ResourceDeleted
			d.lastObject = nil
			d.knownReplicaSets = make(map[string]*appsv1.ReplicaSet)
			d.deletedReplicaSetUIDs = make(map[types.UID]struct{})
			d.reportedReplicaSetFailures = make(map[types.UID]struct{})
			d.failedFromReplicaSetUID = ""
			d.podStatuses = make(map[string]pod.PodStatus)
			d.rsNameByPod = make(map[string]string)
			d.TrackedPodsNames = nil
			d.Status <- DeploymentStatus{}

		case failure := <-d.resourceFailed:
			switch failure := failure.(type) {
			case string:
				// The events informer outlives the object: with no live object
				// there is no status to report the failure on.
				if d.lastObject == nil {
					break
				}

				if err := d.handleResourceFailure(ctx, resourceFailure{reason: failure, mode: FailureModeCounted}); err != nil {
					return err
				}
			default:
				panic(fmt.Errorf("unexpected type %T", failure))
			}

		case rs := <-d.replicaSetAdded:
			if d.isStaleReplicaSet(rs) {
				break
			}

			d.knownReplicaSets[rs.Name] = rs
			d.rearmReplicaSetFailure(rs)

			if d.lastObject != nil {
				rsNew, err := utils.IsReplicaSetNew(d.lastObject, d.knownReplicaSets, rs.Name)
				if err != nil {
					return err
				}

				status, err := d.newStatus(d.lastObject)
				if err != nil {
					return err
				}

				d.AddedReplicaSet <- ReplicaSetAddedReport{
					ReplicaSet: replicaset.ReplicaSet{
						Name:  rs.Name,
						IsNew: rsNew,
					},
					DeploymentStatus: status,
				}

				if err := d.handleNewReplicaSetFailure(ctx); err != nil {
					return err
				}
			}

		case rs := <-d.replicaSetModified:
			if d.isStaleReplicaSet(rs) {
				break
			}

			// A same-named successor may already be known: applying an older
			// incarnation would resurrect the dead one.
			if known, found := d.knownReplicaSets[rs.Name]; !found || known.UID == rs.UID {
				d.knownReplicaSets[rs.Name] = rs
				d.rearmReplicaSetFailure(rs)

				if d.lastObject != nil {
					if err := d.handleNewReplicaSetFailure(ctx); err != nil {
						return err
					}
				}
			}

		case rs := <-d.replicaSetDeleted:
			d.deletedReplicaSetUIDs[rs.UID] = struct{}{}

			// Dropping the ReplicaSet by name alone would lose a live successor.
			if known, found := d.knownReplicaSets[rs.Name]; found && known.UID == rs.UID {
				delete(d.knownReplicaSets, rs.Name)
			}
			delete(d.reportedReplicaSetFailures, rs.UID)

			d.clearReplicaSetFailure(rs.UID)

			// Deletion may promote an already failing ReplicaSet to the new one:
			// its failure was suppressed before and nothing else would report it.
			if d.lastObject != nil {
				if err := d.handleNewReplicaSetFailure(ctx); err != nil {
					return err
				}
			}

		case rs := <-d.replicaSetUnselected:
			if known, found := d.knownReplicaSets[rs.Name]; found && known.UID == rs.UID {
				delete(d.knownReplicaSets, rs.Name)
			}
			delete(d.reportedReplicaSetFailures, rs.UID)
			d.clearReplicaSetFailure(rs.UID)

			if d.lastObject != nil {
				if err := d.handleNewReplicaSetFailure(ctx); err != nil {
					return err
				}
			}

		case <-d.replicaSetSynced:
			d.replicaSetsSynced = true
			if err := d.handleNewReplicaSetFailure(ctx); err != nil {
				return err
			}

		case pod := <-d.podAddedRelay:
			rsName := utils.GetPodReplicaSetName(pod)
			d.rsNameByPod[pod.Name] = rsName

			if d.lastObject != nil {
				rsNew, err := utils.IsReplicaSetNew(d.lastObject, d.knownReplicaSets, rsName)
				if err != nil {
					return err
				}
				if len(d.knownReplicaSets) == 0 {
					rsNew = true
				}

				status, err := d.newStatus(d.lastObject)
				if err != nil {
					return err
				}

				d.AddedPod <- PodAddedReport{
					ReplicaSetPod: replicaset.ReplicaSetPod{
						Name: pod.Name,
						ReplicaSet: replicaset.ReplicaSet{
							Name:  rsName,
							IsNew: rsNew,
						},
					},
					DeploymentStatus: status,
				}
			}

			if err := d.runPodTracker(ctx, pod.Name, rsName); err != nil {
				return err
			}

		case donePods := <-d.donePodsRelay:
			var trackedPodsNames []string

		trackedPodsIteration:
			for _, name := range d.TrackedPodsNames {
				for donePodName, status := range donePods {
					if name == donePodName {
						// This Pod is no more tracked,
						// but we need to update final
						// Pod's status
						if _, hasKey := d.podStatuses[name]; hasKey {
							d.podStatuses[name] = status
						}
						continue trackedPodsIteration
					}
				}

				trackedPodsNames = append(trackedPodsNames, name)
			}
			d.TrackedPodsNames = trackedPodsNames

			if d.lastObject != nil {
				cleanupFn, err := d.handleDeploymentState(ctx, d.lastObject)
				if err != nil {
					return err
				}
				defer cleanupFn()
			}

		case podStatuses := <-d.podStatusesRelay:
			for podName, podStatus := range podStatuses {
				d.podStatuses[podName] = podStatus
			}
			if d.lastObject != nil {
				cleanupFn, err := d.handleDeploymentState(ctx, d.lastObject)
				if err != nil {
					return err
				}
				defer cleanupFn()
			}

		case podLogChunks := <-d.podLogChunksRelay:
			for podName, chunk := range podLogChunks {
				if d.lastObject != nil {
					rsName, hasKey := d.rsNameByPod[podName]
					if !hasKey {
						continue
					}

					rsNew, err := utils.IsReplicaSetNew(d.lastObject, d.knownReplicaSets, rsName)
					if err != nil {
						return err
					}
					if len(d.knownReplicaSets) == 0 {
						rsNew = true
					}

					rsChunk := &replicaset.ReplicaSetPodLogChunk{
						PodLogChunk: &pod.PodLogChunk{
							ContainerLogChunk: chunk,
							PodName:           podName,
						},
						ReplicaSet: replicaset.ReplicaSet{
							Name:  rsName,
							IsNew: rsNew,
						},
					}
					d.PodLogChunk <- rsChunk
				}
			}

		case podContainerErrors := <-d.podContainerErrorsRelay:
			for podName, containerError := range podContainerErrors {
				d.podStatuses[podName] = containerError.PodStatus
			}
			if d.lastObject != nil {
				status, err := d.newStatus(d.lastObject)
				if err != nil {
					return err
				}

				for podName, containerError := range podContainerErrors {
					rsName, hasKey := d.rsNameByPod[podName]
					if !hasKey {
						continue
					}

					rsNew, err := utils.IsReplicaSetNew(d.lastObject, d.knownReplicaSets, rsName)
					if err != nil {
						return err
					}
					if len(d.knownReplicaSets) == 0 {
						rsNew = true
					}

					d.PodError <- PodErrorReport{
						ReplicaSetPodError: replicaset.ReplicaSetPodError{
							PodError: pod.PodError{
								ContainerError: containerError.ContainerError,
								PodName:        podName,
							},
							ReplicaSet: replicaset.ReplicaSet{
								Name:  rsName,
								IsNew: rsNew,
							},
						},
						DeploymentStatus: status,
					}
				}
			}

		case <-ctx.Done():
			if log.Debug() {
				fmt.Printf("Deployment `%s` tracker context canceled: %s\n", d.ResourceName, context.Cause(ctx))
			}

			return context.Cause(ctx)
		case err := <-d.errors:
			return err
		}
	}
}

func (d *Tracker) getNewPodsNames() ([]string, error) {
	res := []string{}

	for podName := range d.podStatuses {
		if rsName, hasKey := d.rsNameByPod[podName]; hasKey {
			if d.lastObject != nil {
				rsNew, err := utils.IsReplicaSetNew(d.lastObject, d.knownReplicaSets, rsName)
				if err != nil {
					return nil, err
				}
				if len(d.knownReplicaSets) == 0 {
					rsNew = true
				}
				if rsNew {
					res = append(res, podName)
				}
			}
		}
	}

	return res, nil
}

// runDeploymentInformer watch for deployment events
func (d *Tracker) runDeploymentInformer(ctx context.Context) (cleanupFn func(), err error) {
	var inform *util.Concurrent[*informer.Informer]
	if err := d.InformerFactory.RWTransactionErr(func(factory *informer.InformerFactory) error {
		inform, err = factory.ForNamespace(schema.GroupVersionResource{
			Group:    "apps",
			Version:  "v1",
			Resource: "deployments",
		}, d.Namespace)
		if err != nil {
			return fmt.Errorf("get informer from factory: %w", err)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	if err := inform.RWTransactionErr(func(inf *informer.Informer) error {
		handler, err := inf.AddEventHandler(
			cache.FilteringResourceEventHandler{
				FilterFunc: func(obj interface{}) bool {
					if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
						obj = d.Obj
					}

					deploymentObj := &appsv1.Deployment{}
					lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, deploymentObj))
					return deploymentObj.Name == d.ResourceName &&
						deploymentObj.Namespace == d.Namespace
				},
				Handler: cache.ResourceEventHandlerFuncs{
					AddFunc: func(obj interface{}) {
						if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
							obj = d.Obj
						}

						deploymentObj := &appsv1.Deployment{}
						lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, deploymentObj))
						d.resourceAdded <- deploymentObj
					},
					UpdateFunc: func(oldObj, newObj interface{}) {
						if d, ok := newObj.(cache.DeletedFinalStateUnknown); ok {
							newObj = d.Obj
						}

						deploymentObj := &appsv1.Deployment{}
						lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(newObj.(*unstructured.Unstructured).Object, deploymentObj))
						d.resourceModified <- deploymentObj
					},
					DeleteFunc: func(obj interface{}) {
						if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
							obj = d.Obj
						}

						deploymentObj := &appsv1.Deployment{}
						lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, deploymentObj))
						d.resourceDeleted <- deploymentObj
					},
				},
			},
		)
		if err != nil {
			return fmt.Errorf("add event handler: %w", err)
		}

		cleanupFn = func() {
			inf.RemoveEventHandler(handler)
		}

		inf.Run()

		return nil
	}); err != nil {
		return nil, err
	}

	return cleanupFn, nil
}

// runReplicaSetsInformer watch for deployment events
func (d *Tracker) runReplicaSetsInformer(ctx context.Context, object *appsv1.Deployment) (cleanupFn func(), err error) {
	rsInformer := replicaset.NewReplicaSetInformer(&d.Tracker, utils.ControllerAccessor(object))
	rsInformer.WithChannels(d.replicaSetAdded, d.replicaSetModified, d.replicaSetDeleted, d.errors)
	rsInformer.WithUnselectedChannel(d.replicaSetUnselected)
	rsInformer.WithSyncedChannel(d.replicaSetSynced)
	return rsInformer.Run(ctx)
}

// runDeploymentInformer watch for deployment events
func (d *Tracker) runPodsInformer(ctx context.Context, object *appsv1.Deployment) (cleanupFn func(), err error) {
	podsInformer := pod.NewPodsInformer(&d.Tracker, utils.ControllerAccessor(object))
	podsInformer.WithChannels(d.podAddedRelay, d.errors)
	return podsInformer.Run(ctx)
}

func (d *Tracker) runPodTracker(_ctx context.Context, podName, rsName string) error {
	errorChan := make(chan error, 1)
	doneChan := make(chan struct{})

	ignoreLogs := d.ignoreLogs || d.savingLogsReplicas >= d.SaveLogsOnlyForNumberOfReplicas
	if !ignoreLogs {
		d.savingLogsReplicas++
	}

	newCtx, cancelPodCtx := context.WithCancelCause(_ctx)
	podTracker := pod.NewTracker(podName, d.Namespace, d.Kube, d.InformerFactory, pod.Options{
		IgnoreLogs:                               ignoreLogs,
		IgnoreReadinessProbeFailsByContainerName: d.ignoreReadinessProbeFailsByContainerName,
	})
	if !d.LogsFromTime.IsZero() {
		podTracker.LogsFromTime = d.LogsFromTime
	}
	d.TrackedPodsNames = append(d.TrackedPodsNames, podName)

	go func() {
		err := podTracker.Start(newCtx)
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- struct{}{}
		}
	}()

	go func() {
		for {
			select {
			case status := <-podTracker.Added:
				d.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}
			case status := <-podTracker.Succeeded:
				d.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}
				cancelPodCtx(fmt.Errorf("context canceled: got succeeded event for %q", podTracker.FullResourceName))
			case status := <-podTracker.Deleted:
				d.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}
				cancelPodCtx(fmt.Errorf("context canceled: got deleted event for %q", podTracker.FullResourceName))
			case report := <-podTracker.Failed:
				d.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: report.PodStatus}
			case status := <-podTracker.Ready:
				d.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}
			case status := <-podTracker.Status:
				d.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}

			case msg := <-podTracker.EventMsg:
				d.EventMsg <- fmt.Sprintf("po/%s %s", podTracker.ResourceName, msg)
			case chunk := <-podTracker.ContainerLogChunk:
				d.podLogChunksRelay <- map[string]*pod.ContainerLogChunk{podTracker.ResourceName: chunk}
			case report := <-podTracker.ContainerError:
				d.podContainerErrorsRelay <- map[string]pod.ContainerErrorReport{podTracker.ResourceName: report}

			case err := <-errorChan:
				if log.Debug() {
					fmt.Printf("received pod %q error chan %v\n", podTracker.ResourceName, err)
				}

				d.errors <- err
				return
			case <-doneChan:
				d.donePodsRelay <- map[string]pod.PodStatus{podTracker.ResourceName: podTracker.LastStatus}
				return
			}
		}
	}()

	return nil
}

func (d *Tracker) handleDeploymentState(ctx context.Context, object *appsv1.Deployment) (cleanupFn func(), err error) {
	d.lastObject = object

	status, err := d.newStatus(object)
	if err != nil {
		return nil, err
	}

	cleanupFn = func() {}

	switch d.State {
	case tracker.Initial:
		replicasetsInformerCleanupFn, err := d.runReplicaSetsInformer(ctx, object)
		if err != nil {
			return nil, fmt.Errorf("run replicaset informer: %w", err)
		}

		// TODO: If pod events handled before any replicasets found, then during the handling we can't determine whether the pod is for the new or for the old replicaset. Needs some proper solution instead of time.Sleep.
		time.Sleep(1500 * time.Millisecond)

		podsInformerCleanupFn, err := d.runPodsInformer(ctx, object)
		if err != nil {
			// An informer left running has no consumer: its callbacks park forever
			// on the unbuffered channels of a tracker that never starts.
			replicasetsInformerCleanupFn()
			return nil, fmt.Errorf("run pods informer: %w", err)
		}

		eventsInformerCleanupFn := func() {}
		if os.Getenv("KUBEDOG_DISABLE_EVENTS") != "1" {
			eventsInformerCleanupFn, err = d.runEventsInformer(ctx, object)
			if err != nil {
				replicasetsInformerCleanupFn()
				podsInformerCleanupFn()
				return nil, fmt.Errorf("run events informer: %w", err)
			}
		}

		cleanupFn = func() {
			replicasetsInformerCleanupFn()
			podsInformerCleanupFn()
			eventsInformerCleanupFn()
		}

		switch {
		case status.IsReady:
			d.State = tracker.ResourceReady
			d.Ready <- status
		case status.IsFailed:
			d.State = tracker.ResourceFailed
			d.Failed <- status
		default:
			d.State = tracker.ResourceAdded
			d.Added <- status
		}
	case tracker.ResourceAdded, tracker.ResourceFailed:
		switch {
		case status.IsReady:
			d.State = tracker.ResourceReady
			d.Ready <- status
		case status.IsFailed:
			d.State = tracker.ResourceFailed
			d.Failed <- status
		default:
			d.Status <- status
		}
	case tracker.ResourceSucceeded:
		d.Status <- status
	case tracker.ResourceDeleted:
		switch {
		case status.IsReady:
			d.State = tracker.ResourceReady
			d.Ready <- status
		case status.IsFailed:
			d.State = tracker.ResourceFailed
			d.Failed <- status
		default:
			d.State = tracker.ResourceAdded
			d.Added <- status
		}
	}

	return cleanupFn, nil
}

// runEventsInformer watch for Deployment events
func (d *Tracker) runEventsInformer(ctx context.Context, resource interface{}) (cleanupFn func(), err error) {
	eventInformer := event.NewEventInformer(&d.Tracker, resource)
	eventInformer.WithChannels(d.EventMsg, d.resourceFailed, d.errors)
	return eventInformer.Run(ctx)
}

// newStatus deliberately leaves the sticky tracker failure out: a failure is reported
// once by handleResourceFailure, and restamping it into every update reports it again.
func (d *Tracker) newStatus(object *appsv1.Deployment) (DeploymentStatus, error) {
	d.StatusGeneration++

	newPodsNames, err := d.getNewPodsNames()
	if err != nil {
		return DeploymentStatus{}, err
	}

	return NewDeploymentStatus(object, d.StatusGeneration, false, "", d.podStatuses, newPodsNames), nil
}
