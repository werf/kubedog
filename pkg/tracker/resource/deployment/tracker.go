package deployment

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"

	"github.com/werf/kubedog/pkg/dyntracker/util"
	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/log"
	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/tracker/event"
	"github.com/werf/kubedog/pkg/tracker/pod"
	"github.com/werf/kubedog/pkg/tracker/resource"
	"github.com/werf/kubedog/pkg/utils"
)

var (
	deploymentGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	replicaSetGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}
	podGVR        = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
)

// podsWatchDelay limits how long the Pods watch waits for the ReplicaSets watch to sync.
// Pods of unknown ReplicaSets can't be told new from old, so the Pods are watched after the
// ReplicaSets are known, but the tracking must not stall if the ReplicaSets never sync.
const podsWatchDelay = 1500 * time.Millisecond

type Options struct {
	LogsFromTime                             time.Time
	SaveLogsOnlyForNumberOfReplicas          int
	IgnoreLogs                               bool
	IgnoreReadinessProbeFailsByContainerName map[string]time.Duration
}

// Tracker tracks the rollout of a Deployment: the Deployment itself, its ReplicaSets and
// its Pods. It reports what it observes as Events and does not present them in any way.
type Tracker struct {
	name            string
	namespace       string
	kube            kubernetes.Interface
	informerFactory *util.Concurrent[*informer.InformerFactory]
	opts            Options

	events chan Event

	state              tracker.TrackerState
	generation         uint64
	object             *appsv1.Deployment
	failedReason       string
	replicaSets        map[string]*appsv1.ReplicaSet
	podStatuses        map[string]pod.PodStatus
	rsNameByPod        map[string]string
	trackedPods        []string
	savingLogsReplicas int
	podsSelector       labels.Selector
	podsCleanupFn      func()

	deploymentEvents chan resource.ObjectEvent[appsv1.Deployment]
	replicaSetEvents chan resource.ObjectEvent[appsv1.ReplicaSet]
	podEvents        chan resource.ObjectEvent[corev1.Pod]
	podUpdates       chan podUpdate
	messages         chan string
	failures         chan interface{}
	errors           chan error
}

// podUpdate is what a child Pod tracker relays to the Tracker. Only one of the fields is set.
type podUpdate struct {
	name           string
	status         *pod.PodStatus
	logChunk       *pod.ContainerLogChunk
	containerError *pod.ContainerErrorReport
	message        string
	done           *pod.PodStatus
}

func NewTracker(name, namespace string, kube kubernetes.Interface, informerFactory *util.Concurrent[*informer.InformerFactory], opts Options) *Tracker {
	return &Tracker{
		name:            name,
		namespace:       namespace,
		kube:            kube,
		informerFactory: informerFactory,
		opts:            opts,

		events: make(chan Event, 100),

		replicaSets: make(map[string]*appsv1.ReplicaSet),
		podStatuses: make(map[string]pod.PodStatus),
		rsNameByPod: make(map[string]string),

		deploymentEvents: make(chan resource.ObjectEvent[appsv1.Deployment], 1),
		replicaSetEvents: make(chan resource.ObjectEvent[appsv1.ReplicaSet], 10),
		podEvents:        make(chan resource.ObjectEvent[corev1.Pod], 10),
		podUpdates:       make(chan podUpdate, 100),
		messages:         make(chan string, 1),
		failures:         make(chan interface{}, 1),
		errors:           make(chan error, 1),
	}
}

func (t *Tracker) Events() <-chan Event {
	return t.events
}

// Track tracks the Deployment until the context is canceled or an error occurs.
func (t *Tracker) Track(ctx context.Context) error {
	cleanupFn, err := resource.Watch(ctx, t.informerFactory, deploymentGVR, resource.WatchOptions{
		Namespace: t.namespace,
		Name:      t.name,
	}, sendTo(ctx, t.deploymentEvents), t.reportError)
	if err != nil {
		return fmt.Errorf("watch deployment: %w", err)
	}
	defer cleanupFn()
	defer t.stopPodsWatch()

	var podsWatchTimer <-chan time.Time

	for {
		select {
		case ev := <-t.deploymentEvents:
			switch ev.Type {
			case resource.EventAdded, resource.EventModified:
				if t.state == tracker.Initial {
					cleanupFn, err := t.watchChildren(ctx, ev.Object)
					if err != nil {
						return err
					}
					defer cleanupFn()

					podsWatchTimer = time.After(podsWatchDelay)
				}

				if err := t.handleDeployment(ctx, ev.Object); err != nil {
					return err
				}
			case resource.EventDeleted:
				t.state = tracker.ResourceDeleted
				t.object = nil
				t.replicaSets = make(map[string]*appsv1.ReplicaSet)
				t.podStatuses = make(map[string]pod.PodStatus)
				t.rsNameByPod = make(map[string]string)
				t.trackedPods = nil

				if err := t.emit(ctx, DeletedEvent{}); err != nil {
					return err
				}
			}

		case ev := <-t.replicaSetEvents:
			switch ev.Type {
			case resource.EventAdded:
				if err := t.handleReplicaSetAdded(ctx, ev.Object); err != nil {
					return err
				}
			case resource.EventModified:
				t.replicaSets[ev.Object.Name] = ev.Object
			case resource.EventDeleted:
				delete(t.replicaSets, ev.Object.Name)
			case resource.EventSynced:
				if err := t.startPodsWatch(ctx); err != nil {
					return err
				}
			}

		case <-podsWatchTimer:
			podsWatchTimer = nil

			if err := t.startPodsWatch(ctx); err != nil {
				return err
			}

		case ev := <-t.podEvents:
			if ev.Type != resource.EventAdded {
				continue
			}

			if err := t.handlePodAdded(ctx, ev.Object); err != nil {
				return err
			}

		case update := <-t.podUpdates:
			if err := t.handlePodUpdate(ctx, update); err != nil {
				return err
			}

		case msg := <-t.messages:
			if err := t.emit(ctx, MessageEvent{Message: msg}); err != nil {
				return err
			}

		case failure := <-t.failures:
			reason, ok := failure.(string)
			if !ok {
				return fmt.Errorf("unexpected failure type %T", failure)
			}

			if err := t.handleFailure(ctx, reason); err != nil {
				return err
			}

		case <-ctx.Done():
			if log.Debug() {
				fmt.Printf("Deployment `%s` tracker context canceled: %s\n", t.name, context.Cause(ctx))
			}

			return context.Cause(ctx)

		case err := <-t.errors:
			return err
		}
	}
}

func (t *Tracker) handleDeployment(ctx context.Context, object *appsv1.Deployment) error {
	t.object = object

	status, err := t.newStatus()
	if err != nil {
		return err
	}

	var transition resource.Transition
	t.state, transition = resource.NextState(t.state, status.Ready, status.Failed)

	switch transition {
	case resource.TransitionAdded:
		return t.emit(ctx, AddedEvent{Status: status})
	case resource.TransitionReady:
		return t.emit(ctx, ReadyEvent{Status: status})
	case resource.TransitionFailed:
		return t.emit(ctx, FailedEvent{Status: status})
	case resource.TransitionStatus:
		return t.emit(ctx, StatusEvent{Status: status})
	}

	return nil
}

func (t *Tracker) handleFailure(ctx context.Context, reason string) error {
	t.state = tracker.ResourceFailed
	t.failedReason = reason

	if t.object == nil {
		return t.emit(ctx, FailedEvent{Status: Status{Failed: true, FailedReason: reason}})
	}

	status, err := t.newStatus()
	if err != nil {
		return err
	}

	return t.emit(ctx, FailedEvent{Status: status})
}

func (t *Tracker) handleReplicaSetAdded(ctx context.Context, rs *appsv1.ReplicaSet) error {
	t.replicaSets[rs.Name] = rs

	if t.object == nil {
		return nil
	}

	replicaSet, err := t.replicaSet(rs.Name)
	if err != nil {
		return err
	}

	status, err := t.newStatus()
	if err != nil {
		return err
	}

	return t.emit(ctx, ReplicaSetAddedEvent{ReplicaSet: replicaSet, Status: status})
}

func (t *Tracker) handlePodAdded(ctx context.Context, object *corev1.Pod) error {
	rsName := utils.GetPodReplicaSetName(object)
	t.rsNameByPod[object.Name] = rsName

	if t.object != nil {
		status, err := t.newStatus()
		if err != nil {
			return err
		}

		replicaSet, err := t.replicaSet(rsName)
		if err != nil {
			return err
		}

		if err := t.emit(ctx, PodAddedEvent{PodName: object.Name, ReplicaSet: replicaSet, Status: status}); err != nil {
			return err
		}
	}

	t.runPodTracker(ctx, object.Name)

	return nil
}

func (t *Tracker) handlePodUpdate(ctx context.Context, update podUpdate) error {
	switch {
	case update.status != nil:
		t.podStatuses[update.name] = *update.status

		if t.object != nil {
			return t.handleDeployment(ctx, t.object)
		}
	case update.done != nil:
		// The Pod is no longer tracked, but its final status still has to be saved.
		if _, found := t.podStatuses[update.name]; found {
			t.podStatuses[update.name] = *update.done
		}
		t.trackedPods = lo.Without(t.trackedPods, update.name)

		if t.object != nil {
			return t.handleDeployment(ctx, t.object)
		}
	case update.logChunk != nil:
		rsName, found := t.rsNameByPod[update.name]
		if t.object == nil || !found {
			return nil
		}

		replicaSet, err := t.replicaSet(rsName)
		if err != nil {
			return err
		}

		return t.emit(ctx, PodLogChunkEvent{PodName: update.name, ReplicaSet: replicaSet, Chunk: update.logChunk})
	case update.containerError != nil:
		t.podStatuses[update.name] = update.containerError.PodStatus

		rsName, found := t.rsNameByPod[update.name]
		if t.object == nil || !found {
			return nil
		}

		status, err := t.newStatus()
		if err != nil {
			return err
		}

		replicaSet, err := t.replicaSet(rsName)
		if err != nil {
			return err
		}

		return t.emit(ctx, PodErrorEvent{
			PodName:        update.name,
			ReplicaSet:     replicaSet,
			ContainerError: update.containerError.ContainerError,
			Status:         status,
		})
	case update.message != "":
		return t.emit(ctx, MessageEvent{Message: fmt.Sprintf("po/%s %s", update.name, update.message)})
	}

	return nil
}

func (t *Tracker) newStatus() (Status, error) {
	t.generation++

	ready, waitingFor := Evaluate(t.object)

	newPodsNames, err := t.newPodsNames()
	if err != nil {
		return Status{}, err
	}

	status := Status{
		Generation:   t.generation,
		Object:       t.object,
		Ready:        ready,
		WaitingFor:   waitingFor,
		Pods:         lo.Assign(t.podStatuses),
		NewPodsNames: newPodsNames,
	}

	if !ready && t.state == tracker.ResourceFailed {
		status.Failed = true
		status.FailedReason = t.failedReason
	}

	return status, nil
}

func (t *Tracker) newPodsNames() ([]string, error) {
	res := []string{}

	for podName := range t.podStatuses {
		rsName, found := t.rsNameByPod[podName]
		if !found {
			continue
		}

		replicaSet, err := t.replicaSet(rsName)
		if err != nil {
			return nil, err
		}

		if replicaSet.IsNew {
			res = append(res, podName)
		}
	}

	return res, nil
}

func (t *Tracker) replicaSet(name string) (ReplicaSet, error) {
	if len(t.replicaSets) == 0 {
		return ReplicaSet{Name: name, IsNew: true}, nil
	}

	isNew, err := utils.IsReplicaSetNew(t.object, t.replicaSets, name)
	if err != nil {
		return ReplicaSet{}, fmt.Errorf("check if replicaset %q is new: %w", name, err)
	}

	return ReplicaSet{Name: name, IsNew: isNew}, nil
}

// watchChildren starts watching the ReplicaSets of the Deployment and the Kubernetes events
// about it. Pods are watched after the ReplicaSets are known, see startPodsWatch.
func (t *Tracker) watchChildren(ctx context.Context, object *appsv1.Deployment) (cleanupFn func(), err error) {
	selector, err := metav1.LabelSelectorAsSelector(utils.ControllerAccessor(object).LabelSelector())
	if err != nil {
		return nil, fmt.Errorf("convert label selector: %w", err)
	}

	rsCleanupFn, err := resource.Watch(ctx, t.informerFactory, replicaSetGVR, resource.WatchOptions{
		Namespace: t.namespace,
		Selector:  selector,
	}, sendTo(ctx, t.replicaSetEvents), t.reportError)
	if err != nil {
		return nil, fmt.Errorf("watch replicasets: %w", err)
	}

	eventsCleanupFn := func() {}
	if os.Getenv("KUBEDOG_DISABLE_EVENTS") != "1" {
		eventInformer := event.NewEventInformer(&tracker.Tracker{
			Kube:             t.kube,
			Namespace:        t.namespace,
			ResourceName:     t.name,
			FullResourceName: fmt.Sprintf("deploy/%s", t.name),
			InformerFactory:  t.informerFactory,
		}, object)
		eventInformer.WithChannels(t.messages, t.failures, t.errors)

		eventsCleanupFn, err = eventInformer.Run(ctx)
		if err != nil {
			rsCleanupFn()
			return nil, fmt.Errorf("run events informer: %w", err)
		}
	}

	t.podsSelector = selector

	return func() {
		rsCleanupFn()
		eventsCleanupFn()
	}, nil
}
