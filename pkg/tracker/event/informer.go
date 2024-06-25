package event

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"

	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/tracker/debug"
	"github.com/werf/kubedog/pkg/utils"
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
	if debug.Debug() {
		fmt.Printf("> NewEventInformer for %s\n", trk.FullResourceName)
	}

	return &EventInformer{
		Tracker: tracker.Tracker{
			Kube:             trk.Kube,
			Namespace:        trk.Namespace,
			FullResourceName: trk.FullResourceName,
		},
		Resource:         resource,
		Errors:           make(chan error),
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
func (e *EventInformer) Run(ctx context.Context) {
	e.handleInitialEvents(ctx)

	client := e.Kube

	tweakEventListOptions := func(options metav1.ListOptions) metav1.ListOptions {
		options.FieldSelector = utils.EventFieldSelectorFromResource(e.Resource)
		return options
	}

	lwe := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return client.CoreV1().Events(e.Namespace).List(ctx, tweakEventListOptions(options))
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return client.CoreV1().Events(e.Namespace).Watch(ctx, tweakEventListOptions(options))
		},
	}

	go func() {
		if debug.Debug() {
			fmt.Printf("> %s run event informer\n", e.FullResourceName)
		}
		_, err := watchtools.UntilWithSync(ctx, lwe, &corev1.Event{}, nil, func(ev watch.Event) (bool, error) {
			if debug.Debug() {
				fmt.Printf("    %s event: %#v\n", e.FullResourceName, ev.Type)
			}

			var object *corev1.Event

			if ev.Type != watch.Error {
				var ok bool
				object, ok = ev.Object.(*corev1.Event)
				if !ok {
					return true, fmt.Errorf("TRACK EVENT expect *corev1.Event object, got %T", ev.Object)
				}
			}

			switch ev.Type {
			case watch.Added:
				e.handleEvent(object)
				// if debug.Debug() {
				//	fmt.Printf("> Event: %#v\n", object)
				// }
			case watch.Modified:
				e.handleEvent(object)
				// if debug.Debug() {
				//	fmt.Printf("> Event: %#v\n", object)
				// }
			case watch.Deleted:
				// if debug.Debug() {
				//	fmt.Printf("> Event: %#v\n", object)
				// }
			case watch.Error:
				return true, fmt.Errorf("event watch error: %v", ev.Object)
			}

			return false, nil
		})

		if err := tracker.AdaptInformerError(err); err != nil {
			e.Errors <- fmt.Errorf("event informer for %s failed: %w", e.FullResourceName, err)
		}

		if debug.Debug() {
			fmt.Printf("     %s event informer DONE\n", e.FullResourceName)
		}
	}()
}

// handleInitialEvents saves uids of existed k8s events to ignore watch.Added events on them
func (e *EventInformer) handleInitialEvents(ctx context.Context) {
	evList, err := utils.ListEventsForObject(ctx, e.Kube, e.Resource)
	if err != nil {
		if debug.Debug() {
			fmt.Printf("list event error: %v\n", err)
		}
		return
	}
	if debug.Debug() {
		utils.DescribeEvents(evList)
	}

	for _, ev := range evList.Items {
		e.initialEventUids[ev.UID] = true
	}
}

// handleEvent sends a message to Messages channel for all events and a message to Failures channel for Failed events
func (e *EventInformer) handleEvent(event *corev1.Event) {
	uid := event.UID
	msg := fmt.Sprintf("%s: %s", event.Reason, event.Message)

	if _, ok := e.initialEventUids[uid]; ok {
		if debug.Debug() {
			fmt.Printf("IGNORE initial event: %s\n", msg)
		}
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
