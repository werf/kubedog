package deployment

import (
	"context"
	"fmt"
	"os"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"

	"github.com/werf/kubedog-for-werf-helm/pkg/tracker"
	"github.com/werf/kubedog-for-werf-helm/pkg/tracker/debug"
	"github.com/werf/kubedog-for-werf-helm/pkg/tracker/event"
	"github.com/werf/kubedog-for-werf-helm/pkg/tracker/pod"
	"github.com/werf/kubedog-for-werf-helm/pkg/tracker/replicaset"
	"github.com/werf/kubedog-for-werf-helm/pkg/utils"
)

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

	knownReplicaSets map[string]*appsv1.ReplicaSet
	lastObject       *appsv1.Deployment
	failedReason     string
	podStatuses      map[string]pod.PodStatus
	rsNameByPod      map[string]string

	ignoreReadinessProbeFailsByContainerName map[string]time.Duration

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

	resourceAdded      chan *appsv1.Deployment
	resourceModified   chan *appsv1.Deployment
	resourceDeleted    chan *appsv1.Deployment
	resourceFailed     chan interface{}
	replicaSetAdded    chan *appsv1.ReplicaSet
	replicaSetModified chan *appsv1.ReplicaSet
	replicaSetDeleted  chan *appsv1.ReplicaSet
	errors             chan error

	podAddedRelay           chan *corev1.Pod
	podStatusesRelay        chan map[string]pod.PodStatus
	podLogChunksRelay       chan map[string]*pod.ContainerLogChunk
	podContainerErrorsRelay chan map[string]pod.ContainerErrorReport
	donePodsRelay           chan map[string]pod.PodStatus
}

func NewTracker(name, namespace string, kube kubernetes.Interface, opts tracker.Options) *Tracker {
	return &Tracker{
		Tracker: tracker.Tracker{
			Kube:             kube,
			Namespace:        namespace,
			FullResourceName: fmt.Sprintf("deploy/%s", name),
			ResourceName:     name,
			LogsFromTime:     opts.LogsFromTime,
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

		knownReplicaSets: make(map[string]*appsv1.ReplicaSet),
		podStatuses:      make(map[string]pod.PodStatus),
		rsNameByPod:      make(map[string]string),

		ignoreReadinessProbeFailsByContainerName: opts.IgnoreReadinessProbeFailsByContainerName,

		errors:             make(chan error),
		resourceAdded:      make(chan *appsv1.Deployment, 1),
		resourceModified:   make(chan *appsv1.Deployment, 1),
		resourceDeleted:    make(chan *appsv1.Deployment, 1),
		resourceFailed:     make(chan interface{}, 1),
		replicaSetAdded:    make(chan *appsv1.ReplicaSet, 1),
		replicaSetModified: make(chan *appsv1.ReplicaSet, 1),
		replicaSetDeleted:  make(chan *appsv1.ReplicaSet, 1),

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
	d.runDeploymentInformer(ctx)

	for {
		select {
		case object := <-d.resourceAdded:
			if err := d.handleDeploymentState(ctx, object); err != nil {
				return err
			}

		case object := <-d.resourceModified:
			if err := d.handleDeploymentState(ctx, object); err != nil {
				return err
			}

		case <-d.resourceDeleted:
			d.State = tracker.ResourceDeleted
			d.lastObject = nil
			d.knownReplicaSets = make(map[string]*appsv1.ReplicaSet)
			d.podStatuses = make(map[string]pod.PodStatus)
			d.rsNameByPod = make(map[string]string)
			d.TrackedPodsNames = nil
			d.Status <- DeploymentStatus{}

		case failure := <-d.resourceFailed:
			switch failure := failure.(type) {
			case string:
				d.State = tracker.ResourceFailed
				d.failedReason = failure

				var status DeploymentStatus
				if d.lastObject != nil {
					d.StatusGeneration++
					newPodsNames, err := d.getNewPodsNames()
					if err != nil {
						return err
					}
					status = NewDeploymentStatus(d.lastObject, d.StatusGeneration, d.State == tracker.ResourceFailed, d.failedReason, d.podStatuses, newPodsNames)
				} else {
					status = DeploymentStatus{IsFailed: true, FailedReason: failure}
				}
				d.Failed <- status
			default:
				panic(fmt.Errorf("unexpected type %T", failure))
			}

		case rs := <-d.replicaSetAdded:
			d.knownReplicaSets[rs.Name] = rs

			if d.lastObject != nil {
				rsNew, err := utils.IsReplicaSetNew(d.lastObject, d.knownReplicaSets, rs.Name)
				if err != nil {
					return err
				}
				if len(d.knownReplicaSets) == 0 {
					rsNew = true
				}

				d.StatusGeneration++
				newPodsNames, err := d.getNewPodsNames()
				if err != nil {
					return err
				}
				status := NewDeploymentStatus(d.lastObject, d.StatusGeneration, d.State == tracker.ResourceFailed, d.failedReason, d.podStatuses, newPodsNames)

				d.AddedReplicaSet <- ReplicaSetAddedReport{
					ReplicaSet: replicaset.ReplicaSet{
						Name:  rs.Name,
						IsNew: rsNew,
					},
					DeploymentStatus: status,
				}
			}

		case rs := <-d.replicaSetModified:
			d.knownReplicaSets[rs.Name] = rs

		case rs := <-d.replicaSetDeleted:
			delete(d.knownReplicaSets, rs.Name)

		case pod := <-d.podAddedRelay:
			rsName := utils.GetPodReplicaSetName(pod)
			d.rsNameByPod[pod.Name] = rsName

			if d.lastObject != nil {
				d.StatusGeneration++
				newPodsNames, err := d.getNewPodsNames()
				if err != nil {
					return err
				}
				rsNew, err := utils.IsReplicaSetNew(d.lastObject, d.knownReplicaSets, rsName)
				if err != nil {
					return err
				}
				if len(d.knownReplicaSets) == 0 {
					rsNew = true
				}
				status := NewDeploymentStatus(d.lastObject, d.StatusGeneration, d.State == tracker.ResourceFailed, d.failedReason, d.podStatuses, newPodsNames)

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
				if err := d.handleDeploymentState(ctx, d.lastObject); err != nil {
					return err
				}
			}

		case podStatuses := <-d.podStatusesRelay:
			for podName, podStatus := range podStatuses {
				d.podStatuses[podName] = podStatus
			}
			if d.lastObject != nil {
				if err := d.handleDeploymentState(ctx, d.lastObject); err != nil {
					return err
				}
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
				d.StatusGeneration++
				newPodsNames, err := d.getNewPodsNames()
				if err != nil {
					return err
				}
				status := NewDeploymentStatus(d.lastObject, d.StatusGeneration, d.State == tracker.ResourceFailed, d.failedReason, d.podStatuses, newPodsNames)

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
			if debug.Debug() {
				fmt.Printf("Deployment %q tracker context Done! -> ctx.Err() -> %v\n", d.ResourceName, ctx.Err())
			}

			if ctx.Err() == context.Canceled {
				return nil
			}
			return ctx.Err()
		case err := <-d.errors:
			if debug.Debug() {
				fmt.Printf("Deployment %q tracker error received! -> %v\n", d.ResourceName, err)
			}

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
func (d *Tracker) runDeploymentInformer(ctx context.Context) {
	client := d.Kube

	tweakListOptions := func(options metav1.ListOptions) metav1.ListOptions {
		options.FieldSelector = fields.OneTermEqualSelector("metadata.name", d.ResourceName).String()
		return options
	}
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return client.AppsV1().Deployments(d.Namespace).List(ctx, tweakListOptions(options))
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return client.AppsV1().Deployments(d.Namespace).Watch(ctx, tweakListOptions(options))
		},
	}

	go func() {
		_, err := watchtools.UntilWithSync(ctx, lw, &appsv1.Deployment{}, nil, func(e watch.Event) (bool, error) {
			if debug.Debug() {
				fmt.Printf("    deploy/%s event: %#v\n", d.ResourceName, e.Type)
			}

			var object *appsv1.Deployment

			if e.Type != watch.Error {
				var ok bool
				object, ok = e.Object.(*appsv1.Deployment)
				if !ok {
					return true, fmt.Errorf("expected %s to be a *extension.Deployment, got %T", d.ResourceName, e.Object)
				}
			}

			switch e.Type {
			case watch.Added:
				d.resourceAdded <- object
			case watch.Modified:
				d.resourceModified <- object
			case watch.Deleted:
				d.resourceDeleted <- object
			case watch.Error:
				err := fmt.Errorf("deployment error: %v", e.Object)
				// d.errors <- err
				return true, err
			}

			return false, nil
		})

		if err := tracker.AdaptInformerError(err); err != nil {
			d.errors <- err
		}

		if debug.Debug() {
			fmt.Printf("      deploy/%s informer DONE\n", d.ResourceName)
		}
	}()
}

// runReplicaSetsInformer watch for deployment events
func (d *Tracker) runReplicaSetsInformer(ctx context.Context, object *appsv1.Deployment) {
	rsInformer := replicaset.NewReplicaSetInformer(&d.Tracker, utils.ControllerAccessor(object))
	rsInformer.WithChannels(d.replicaSetAdded, d.replicaSetModified, d.replicaSetDeleted, d.errors)
	rsInformer.Run(ctx)
}

// runDeploymentInformer watch for deployment events
func (d *Tracker) runPodsInformer(ctx context.Context, object *appsv1.Deployment) {
	podsInformer := pod.NewPodsInformer(&d.Tracker, utils.ControllerAccessor(object))
	podsInformer.WithChannels(d.podAddedRelay, d.errors)
	podsInformer.Run(ctx)
}

func (d *Tracker) runPodTracker(_ctx context.Context, podName, rsName string) error {
	errorChan := make(chan error)
	doneChan := make(chan struct{})

	newCtx, cancelPodCtx := context.WithCancel(_ctx)
	podTracker := pod.NewTracker(podName, d.Namespace, d.Kube, pod.Options{
		IgnoreReadinessProbeFailsByContainerName: d.ignoreReadinessProbeFailsByContainerName,
	})
	if !d.LogsFromTime.IsZero() {
		podTracker.LogsFromTime = d.LogsFromTime
	}
	d.TrackedPodsNames = append(d.TrackedPodsNames, podName)

	go func() {
		if debug.Debug() {
			fmt.Printf("Starting Deployment's `%s` Pod `%s` tracker. pod state: %v\n", d.ResourceName, podTracker.ResourceName, podTracker.State)
		}

		err := podTracker.Start(newCtx)
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- struct{}{}
		}

		if debug.Debug() {
			fmt.Printf("Done Deployment's `%s` Pod `%s` tracker\n", d.ResourceName, podTracker.ResourceName)
		}
	}()

	go func() {
		for {
			select {
			case status := <-podTracker.Added:
				d.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}
			case status := <-podTracker.Succeeded:
				d.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}
				cancelPodCtx()
			case status := <-podTracker.Deleted:
				d.podStatusesRelay <- map[string]pod.PodStatus{podTracker.ResourceName: status}
				cancelPodCtx()
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
				if debug.Debug() {
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

func (d *Tracker) handleDeploymentState(ctx context.Context, object *appsv1.Deployment) error {
	d.lastObject = object
	d.StatusGeneration++

	newPodsNames, err := d.getNewPodsNames()
	if err != nil {
		return err
	}
	status := NewDeploymentStatus(object, d.StatusGeneration, d.State == tracker.ResourceFailed, d.failedReason, d.podStatuses, newPodsNames)

	switch d.State {
	case tracker.Initial:
		d.runReplicaSetsInformer(ctx, object)
		// TODO: If pod events handled before any replicasets found, then during the handling we can't determine whether the pod is for the new or for the old replicaset. Needs some proper solution instead of time.Sleep.
		time.Sleep(1500 * time.Millisecond)
		d.runPodsInformer(ctx, object)

		if os.Getenv("KUBEDOG_DISABLE_EVENTS") != "1" {
			d.runEventsInformer(ctx, object)
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

	return nil
}

// runEventsInformer watch for Deployment events
func (d *Tracker) runEventsInformer(ctx context.Context, resource interface{}) {
	eventInformer := event.NewEventInformer(&d.Tracker, resource)
	eventInformer.WithChannels(d.EventMsg, d.resourceFailed, d.errors)
	eventInformer.Run(ctx)
}
