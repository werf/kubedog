package event

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	apilabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"

	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/tracker/debug"
	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
)

type ProbeTriggeredRestart struct {
	ContainerName string
	Message       string
}

type ReadinessProbeFailure struct {
	ContainerName string
	Message       string
}

type EventInformer struct {
	tracker.Tracker
	Resource interface{}
	Messages chan string
	Failures chan interface{}
	Errors   chan error

	initialEventUids map[types.UID]bool
}

func NewEventInformer(trk *tracker.Tracker, resource interface{}) *EventInformer {
	return &EventInformer{
		Tracker: tracker.Tracker{
			Kube:             trk.Kube,
			Namespace:        trk.Namespace,
			ResourceName:     trk.ResourceName,
			FullResourceName: trk.FullResourceName,
			InformerFactory:  trk.InformerFactory,
		},
		Resource:         resource,
		Errors:           make(chan error, 1),
		initialEventUids: make(map[types.UID]bool),
	}
}

func (e *EventInformer) WithChannels(msgCh chan string, failCh chan interface{}, errors chan error) *EventInformer {
	e.Messages = msgCh
	e.Failures = failCh
	e.Errors = errors
	return e
}

// Run watch for StatefulSet events
func (e *EventInformer) Run(ctx context.Context) (cleanupFn func(), err error) {
	var inform *util.Concurrent[*informer.Informer]
	if err := e.InformerFactory.RWTransactionErr(func(factory *informer.InformerFactory) error {
		inform, err = factory.ForNamespace(schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "events",
		}, e.Namespace)
		if err != nil {
			return fmt.Errorf("get informer from factory: %w", err)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	involvedObjMetadata, err := meta.Accessor(e.Resource)
	if err != nil {
		return nil, fmt.Errorf("get involved object metadata: %w", err)
	}

	if err := inform.RWTransactionErr(func(inf *informer.Informer) error {
		if err := e.handleInitialEvents(inf, string(involvedObjMetadata.GetUID())); err != nil {
			return fmt.Errorf("handle initial events: %w", err)
		}

		handler, err := inf.AddEventHandler(
			cache.FilteringResourceEventHandler{
				FilterFunc: func(obj interface{}) bool {
					eventObj := &corev1.Event{}
					lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, eventObj))
					return eventObj.InvolvedObject.UID == involvedObjMetadata.GetUID()
				},
				Handler: cache.ResourceEventHandlerFuncs{
					AddFunc: func(obj interface{}) {
						eventObj := &corev1.Event{}
						lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, eventObj))
						e.handleEvent(eventObj)
					},
					UpdateFunc: func(oldObj, newObj interface{}) {
						eventObj := &corev1.Event{}
						lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(newObj.(*unstructured.Unstructured).Object, eventObj))
						e.handleEvent(eventObj)
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

// handleInitialEvents saves uids of existed k8s events to ignore watch.Added events on them
func (e *EventInformer) handleInitialEvents(inform *informer.Informer, involvedUID string) error {
	objs, err := inform.List(apilabels.Everything())
	if err != nil {
		return fmt.Errorf("list events: %w", err)
	}

	var filteredEvents []*corev1.Event
	for _, obj := range objs {
		eventObj := &corev1.Event{}
		lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, eventObj))

		if string(eventObj.InvolvedObject.UID) == involvedUID {
			filteredEvents = append(filteredEvents, eventObj)
		}
	}

	for _, event := range filteredEvents {
		e.initialEventUids[event.UID] = true
	}

	return nil
}

// handleEvent sends a message to Messages channel for all events and a message to Failures channel for Failed events
func (e *EventInformer) handleEvent(event *corev1.Event) {
	uid := event.UID
	msg := fmt.Sprintf("%s: %s", event.Reason, event.Message)

	if _, ok := e.initialEventUids[uid]; ok {
		delete(e.initialEventUids, uid)
		return
	}

	if debug.Debug() {
		fmt.Printf("  %s got normal event: %s\n", e.FullResourceName, msg)
	}

	e.Messages <- msg

	switch resource := e.Resource.(type) {
	case *corev1.Pod:
		e.handlePodEvent(event, resource, msg)
	default:
		e.handleRegularEvent(event, msg)
	}
}

func (e *EventInformer) handleRegularEvent(event *corev1.Event, msg string) {
	if strings.Contains(event.Reason, "RecreatingFailedPod") ||
		strings.Contains(event.Reason, "FailedDelete") {
		return
	}

	if strings.Contains(event.Reason, "Failed") {
		if debug.Debug() {
			fmt.Printf("got FAILED EVENT!!! %s\n", msg)
		}
		e.Failures <- msg
	}
}

func (e *EventInformer) handlePodEvent(event *corev1.Event, pod *corev1.Pod, msg string) {
	if pod.ObjectMeta.DeletionTimestamp != nil {
		return
	}

	if matched, err := regexp.MatchString(`failed (startup|liveness) probe, will be restarted`, event.Message); err != nil {
		panic("can't compile regex")
	} else if matched {
		e.Failures <- ProbeTriggeredRestart{
			ContainerName: strings.TrimSuffix(strings.Split(event.InvolvedObject.FieldPath, "{")[1], "}"),
			Message:       msg,
		}
	}

	if strings.Contains(event.Message, "Readiness probe failed:") {
		e.Failures <- ReadinessProbeFailure{
			ContainerName: strings.TrimSuffix(strings.Split(event.InvolvedObject.FieldPath, "{")[1], "}"),
			Message:       msg,
		}
	}
}
