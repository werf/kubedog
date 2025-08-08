package pod

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/werf/kubedog/pkg/display"
	"github.com/werf/kubedog/pkg/informer"
	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/tracker/debug"
	"github.com/werf/kubedog/pkg/tracker/event"
	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
)

type ContainerError struct {
	Message       string
	ContainerName string
}

type ContainerLogChunk struct {
	ContainerName string
	LogLines      []display.LogLine
}

type PodLogChunk struct {
	*ContainerLogChunk
	PodName string
}

type PodError struct {
	ContainerError
	PodName string
}

type FailedReport struct {
	FailedReason string
	PodStatus    PodStatus
}

type ContainerErrorReport struct {
	ContainerError
	PodStatus PodStatus
}

type Tracker struct {
	tracker.Tracker

	Added     chan PodStatus
	Deleted   chan PodStatus
	Succeeded chan PodStatus
	Ready     chan PodStatus
	Failed    chan FailedReport
	Status    chan PodStatus

	EventMsg          chan string
	ContainerLogChunk chan *ContainerLogChunk
	ContainerError    chan ContainerErrorReport

	// LastStatus struct is needed for the Job tracker.
	// LastStatus contains latest known and actual resource status.
	LastStatus PodStatus

	State                        tracker.TrackerState
	ContainerTrackerStates       map[string]tracker.TrackerState
	ContainerTrackerStateChanges map[string]chan tracker.TrackerState

	TrackedContainers []string
	LogsFromTime      time.Time

	ignoreLogs bool

	readinessProbes                          map[string]*ReadinessProbe
	ignoreReadinessProbeFailsByContainerName map[string]time.Duration

	lastObject   *corev1.Pod
	failedReason string

	objectAdded    chan *corev1.Pod
	objectModified chan *corev1.Pod
	objectDeleted  chan *corev1.Pod
	objectFailed   chan interface{}

	containerDone chan string
	errors        chan error
}

type Options struct {
	IgnoreReadinessProbeFailsByContainerName map[string]time.Duration
	IgnoreLogs                               bool
}

func NewTracker(name, namespace string, kube kubernetes.Interface, informerFactory *util.Concurrent[*informer.InformerFactory], opts Options) *Tracker {
	return &Tracker{
		Tracker: tracker.Tracker{
			Kube:             kube,
			Namespace:        namespace,
			FullResourceName: fmt.Sprintf("po/%s", name),
			ResourceName:     name,
			InformerFactory:  informerFactory,
		},

		Added:     make(chan PodStatus, 1),
		Deleted:   make(chan PodStatus),
		Succeeded: make(chan PodStatus),
		Ready:     make(chan PodStatus),
		Failed:    make(chan FailedReport),
		Status:    make(chan PodStatus, 100),

		EventMsg:          make(chan string, 1),
		ContainerError:    make(chan ContainerErrorReport),
		ContainerLogChunk: make(chan *ContainerLogChunk, 1000),

		State:                        tracker.Initial,
		ContainerTrackerStates:       make(map[string]tracker.TrackerState),
		ContainerTrackerStateChanges: make(map[string]chan tracker.TrackerState),
		LogsFromTime:                 time.Time{},

		ignoreLogs: opts.IgnoreLogs,

		readinessProbes:                          make(map[string]*ReadinessProbe),
		ignoreReadinessProbeFailsByContainerName: opts.IgnoreReadinessProbeFailsByContainerName,

		objectAdded:    make(chan *corev1.Pod),
		objectModified: make(chan *corev1.Pod),
		objectDeleted:  make(chan *corev1.Pod),
		objectFailed:   make(chan interface{}, 1),
		errors:         make(chan error, 1),
		containerDone:  make(chan string, 10),
	}
}

func (pod *Tracker) Start(ctx context.Context) error {
	podInformerCleanupFn, err := pod.runInformer(ctx)
	if err != nil {
		return err
	}
	defer podInformerCleanupFn()

	for {
		select {
		case object := <-pod.objectAdded:
			cleanupFn, err := pod.handlePodState(ctx, object)
			if err != nil {
				return err
			}
			defer cleanupFn()
		case object := <-pod.objectModified:
			cleanupFn, err := pod.handlePodState(ctx, object)
			if err != nil {
				return err
			}
			defer cleanupFn()
		case <-pod.objectDeleted:
			for containerName, ch := range pod.ContainerTrackerStateChanges {
				oldState := pod.ContainerTrackerStates[containerName]
				newState := tracker.ContainerTrackerDone

				if oldState != newState {
					pod.ContainerTrackerStates[containerName] = newState
					ch <- newState

					if debug.Debug() {
						fmt.Printf("pod/%s container/%s state changed %v -> %v\n", pod.ResourceName, containerName, oldState, newState)
					}
				}
			}

			pod.State = tracker.ResourceDeleted
			pod.lastObject = nil

			pod.ContainerTrackerStateChanges = make(map[string]chan tracker.TrackerState)
			pod.ContainerTrackerStates = make(map[string]tracker.TrackerState)

			status := PodStatus{}
			pod.LastStatus = status

			if debug.Debug() {
				fmt.Printf("Pod %q deleted status: %#v\n", pod.ResourceName, status)
			}

			pod.Deleted <- status

		case failure := <-pod.objectFailed:
			switch failure := failure.(type) {
			case string:
				pod.handleRegularFailure(failure)
			case event.ProbeTriggeredRestart:
				pod.handleProbeTriggeredRestart(failure)
			case event.ReadinessProbeFailure:
				pod.handleReadinessProbeFailure(failure)
			default:
				panic(fmt.Errorf("unexpected type %T", failure))
			}

		case containerName := <-pod.containerDone:
			trackedContainers := make([]string, 0)
			for _, name := range pod.TrackedContainers {
				if name != containerName {
					trackedContainers = append(trackedContainers, name)
				}
			}
			pod.TrackedContainers = trackedContainers

			if pod.lastObject != nil {
				cleanupFn, err := pod.handlePodState(ctx, pod.lastObject)
				if err != nil {
					return err
				}
				defer cleanupFn()
			}
		case <-ctx.Done():
			if debug.Debug() {
				fmt.Printf("Pod `%s` tracker context canceled: %s\n", pod.ResourceName, context.Cause(ctx))
			}

			return nil
		case err := <-pod.errors:
			if debug.Debug() {
				fmt.Printf("pod tracker %s error received! err=%v\n", pod.ResourceName, err)
			}

			return err
		}
	}
}

func (pod *Tracker) handleRegularFailure(reason string) {
	pod.State = tracker.ResourceFailed
	pod.failedReason = reason

	var status PodStatus
	if pod.lastObject != nil {
		pod.StatusGeneration++
		status = NewPodStatus(pod.lastObject, pod.StatusGeneration, pod.TrackedContainers, pod.State == tracker.ResourceFailed, pod.failedReason)
	} else {
		status = PodStatus{IsFailed: true, FailedReason: reason}
	}

	pod.LastStatus = status
	pod.Failed <- FailedReport{PodStatus: status, FailedReason: reason}
}

func (pod *Tracker) handleProbeTriggeredRestart(event event.ProbeTriggeredRestart) {
	if debug.Debug() {
		fmt.Printf("Container %q of pod %q processing ProbeTriggeredRestart event\n",
			pod.ResourceName, event.ContainerName)
	}
	pod.ContainerError <- ContainerErrorReport{
		ContainerError: ContainerError{
			ContainerName: event.ContainerName,
			Message:       event.Message,
		},
		PodStatus: pod.LastStatus,
	}
}

func (pod *Tracker) handleReadinessProbeFailure(event event.ReadinessProbeFailure) {
	readinessProbe, ok := pod.readinessProbes[event.ContainerName]
	if !ok {
		fmt.Printf("WARNING: Container %q of pod %q has no ReadinessProbe initialized, but ReadinessProbeFailure received: %s\n",
			pod.ResourceName, event.ContainerName, event.Message)
		return
	}

	if readinessProbe.IsFailureShouldBeIgnoredNow() {
		if debug.Debug() {
			fmt.Printf("Container %q of pod %q ignores ReadinessProbeFailure: %s\n",
				pod.ResourceName, event.ContainerName, event.Message)
		}
		return
	}

	if debug.Debug() {
		fmt.Printf("Container %q of pod %q processing ReadinessProbeFailure: %s\n",
			pod.ResourceName, event.ContainerName, event.Message)
	}
	pod.ContainerError <- ContainerErrorReport{
		ContainerError: ContainerError{
			ContainerName: event.ContainerName,
			Message:       event.Message,
		},
		PodStatus: pod.LastStatus,
	}
}

func (pod *Tracker) handlePodState(ctx context.Context, object *corev1.Pod) (cleanupFn func(), err error) {
	pod.lastObject = object
	pod.StatusGeneration++

	status := NewPodStatus(object, pod.StatusGeneration, pod.TrackedContainers, pod.State == tracker.ResourceFailed, pod.failedReason)
	pod.LastStatus = status

	cleanupFn = func() {}
	switch pod.State {
	case tracker.Initial:
		if os.Getenv("KUBEDOG_DISABLE_EVENTS") != "1" {
			pod.setupReadinessProbes()

			cleanupFn, err = pod.runEventsInformer(ctx)
			if err != nil {
				return nil, fmt.Errorf("run events informer for pod %q: %w", pod.ResourceName, err)
			}
		}

		if err := pod.runContainersTrackers(ctx, object); err != nil {
			return nil, fmt.Errorf("unable to start tracking pod/%s containers: %w", pod.ResourceName, err)
		}
	default:
		if os.Getenv("KUBEDOG_DISABLE_EVENTS") != "1" {
			pod.updateReadinessProbes()
		}
	}

	if err := pod.handleContainersState(object); err != nil {
		return nil, fmt.Errorf("unable to handle pod containers state: %w", err)
	}

	for _, containerError := range status.ContainersErrors {
		pod.ContainerError <- ContainerErrorReport{
			ContainerError: ContainerError{
				ContainerName: containerError.ContainerName,
				Message:       containerError.Message,
			},
			PodStatus: status,
		}
	}

	switch pod.State {
	case tracker.Initial:
		switch {
		case status.IsSucceeded:
			pod.State = tracker.ResourceSucceeded
			pod.Succeeded <- status
		case status.IsReady:
			pod.State = tracker.ResourceReady
			pod.Ready <- status
		case status.IsFailed:
			pod.State = tracker.ResourceFailed
			pod.Failed <- FailedReport{PodStatus: status, FailedReason: status.FailedReason}
		default:
			pod.State = tracker.ResourceAdded
			pod.Added <- status
		}
	case tracker.ResourceAdded, tracker.ResourceFailed:
		switch {
		case status.IsSucceeded:
			pod.State = tracker.ResourceSucceeded
			pod.Succeeded <- status
		case status.IsReady:
			pod.State = tracker.ResourceReady
			pod.Ready <- status
		case status.IsFailed:
			pod.State = tracker.ResourceFailed
			pod.Failed <- FailedReport{PodStatus: status, FailedReason: status.FailedReason}
		default:
			pod.Status <- status
		}
	case tracker.ResourceSucceeded:
		pod.Status <- status
	case tracker.ResourceReady:
		switch {
		case status.IsSucceeded:
			pod.State = tracker.ResourceSucceeded
			pod.Succeeded <- status
		case status.IsFailed:
			pod.State = tracker.ResourceFailed
			pod.Failed <- FailedReport{PodStatus: status, FailedReason: status.FailedReason}
		default:
			pod.Status <- status
		}
	case tracker.ResourceDeleted:
		switch {
		case status.IsSucceeded:
			pod.State = tracker.ResourceSucceeded
			pod.Succeeded <- status
		case status.IsReady:
			pod.State = tracker.ResourceReady
			pod.Ready <- status
		case status.IsFailed:
			pod.State = tracker.ResourceFailed
			pod.Failed <- FailedReport{PodStatus: status, FailedReason: status.FailedReason}
		default:
			pod.State = tracker.ResourceAdded
			pod.Added <- status
		}
	}

	return cleanupFn, nil
}

func (pod *Tracker) handleContainersState(object *corev1.Pod) error {
	allContainerStatuses := make([]corev1.ContainerStatus, 0)
	allContainerStatuses = append(allContainerStatuses, object.Status.InitContainerStatuses...)
	allContainerStatuses = append(allContainerStatuses, object.Status.ContainerStatuses...)

	for _, cs := range allContainerStatuses {
		if cs.State.Running != nil || cs.State.Terminated != nil {
			oldState := pod.ContainerTrackerStates[cs.Name]
			newState := lo.Ternary(pod.ignoreLogs, tracker.ContainerTrackerDone, tracker.FollowingContainerLogs)

			if oldState != newState {
				pod.ContainerTrackerStates[cs.Name] = newState
				pod.ContainerTrackerStateChanges[cs.Name] <- newState

				if debug.Debug() {
					fmt.Printf("pod/%s container/%s state changed %v -> %v\n", pod.ResourceName, cs.Name, oldState, newState)
				}
			}
		}
	}

	return nil
}

func (pod *Tracker) followContainerLogs(ctx context.Context, containerName string) error {
	if pod.ignoreLogs {
		return nil
	}

	logOpts := &corev1.PodLogOptions{
		Container:  containerName,
		Timestamps: true,
		Follow:     true,
	}
	if !pod.LogsFromTime.IsZero() {
		logOpts.SinceTime = &metav1.Time{
			Time: pod.LogsFromTime,
		}
	}
	req := pod.Kube.CoreV1().
		Pods(pod.Namespace).
		GetLogs(pod.ResourceName, logOpts)

	readCloser, err := req.Stream(ctx)
	if err != nil {
		return err
	}
	defer readCloser.Close()

	chunkBuf := make([]byte, 1024*64)
	lineBuf := make([]byte, 0, 1024*4)

	for {
		n, err := readCloser.Read(chunkBuf)

		if n > 0 {
			chunkLines := make([]display.LogLine, 0)
			for i := 0; i < n; i++ {
				bt := chunkBuf[i]

				if bt == '\n' {
					line := string(lineBuf)
					lineBuf = lineBuf[:0]

					lineParts := strings.SplitN(line, " ", 2)
					if len(lineParts) == 2 {
						chunkLines = append(chunkLines, display.LogLine{Timestamp: lineParts[0], Message: lineParts[1]})
					}

					continue
				}

				lineBuf = append(lineBuf, bt)
			}

			pod.ContainerLogChunk <- &ContainerLogChunk{
				ContainerName: containerName,
				LogLines:      chunkLines,
			}
		}

		if err == io.EOF {
			break
		}

		if err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			if debug.Debug() {
				fmt.Printf("Follow container logs for pod %q context canceled: %s\n", pod.ResourceName, context.Cause(ctx))
			}

			return nil
		default:
		}
	}

	return nil
}

func (pod *Tracker) trackContainer(ctx context.Context, containerName string, containerTrackerStateChanges chan tracker.TrackerState) error {
	for {
		select {
		case state := <-containerTrackerStateChanges:
			switch state {
			case tracker.FollowingContainerLogs:
				err := pod.followContainerLogs(ctx, containerName)
				if err != nil {
					if debug.Debug() {
						fmt.Fprintf(os.Stderr, "pod/%s container/%s logs streaming error: %s\n", pod.ResourceName, containerName, err)
					}
				}
				return nil

			case tracker.ContainerTrackerDone:
				return nil

			default:
				panic(fmt.Sprintf("unknown pod/%s container/%s tracker state %q", pod.ResourceName, containerName, state))
			}

		case <-ctx.Done():
			if debug.Debug() {
				fmt.Printf("Tracking container for pod `%s` context canceled: %s\n", pod.ResourceName, context.Cause(ctx))
			}

			return nil
		}
	}
}

func (pod *Tracker) runContainersTrackers(ctx context.Context, object *corev1.Pod) error {
	allContainersNames := make([]string, 0)
	for _, containerConf := range object.Spec.InitContainers {
		allContainersNames = append(allContainersNames, containerConf.Name)
	}
	for _, containerConf := range object.Spec.Containers {
		allContainersNames = append(allContainersNames, containerConf.Name)
	}
	for i := range allContainersNames {
		containerName := allContainersNames[i]

		containerTrackerStateChanges := make(chan tracker.TrackerState, 1)
		pod.ContainerTrackerStateChanges[containerName] = containerTrackerStateChanges

		pod.ContainerTrackerStates[containerName] = tracker.Initial

		pod.TrackedContainers = append(pod.TrackedContainers, containerName)

		go func() {
			if err := pod.trackContainer(ctx, containerName, containerTrackerStateChanges); err != nil {
				pod.errors <- err
			}

			pod.containerDone <- containerName
		}()
	}

	return nil
}

func (pod *Tracker) runInformer(ctx context.Context) (cleanupFn func(), err error) {
	var inform *util.Concurrent[*informer.Informer]
	if err := pod.InformerFactory.RWTransactionErr(func(factory *informer.InformerFactory) error {
		inform, err = factory.ForNamespace(schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "pods",
		}, pod.Namespace)
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
					podObj := &corev1.Pod{}
					lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, podObj))
					return podObj.Name == pod.ResourceName &&
						podObj.Namespace == pod.Namespace
				},
				Handler: cache.ResourceEventHandlerFuncs{
					AddFunc: func(obj interface{}) {
						podObj := &corev1.Pod{}
						lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, podObj))
						pod.objectAdded <- podObj
					},
					UpdateFunc: func(oldObj, newObj interface{}) {
						podObj := &corev1.Pod{}
						lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(newObj.(*unstructured.Unstructured).Object, podObj))
						pod.objectModified <- podObj
					},
					DeleteFunc: func(obj interface{}) {
						podObj := &corev1.Pod{}
						lo.Must0(runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).Object, podObj))
						pod.objectDeleted <- podObj
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

// runEventsInformer watch for DaemonSet events
func (pod *Tracker) runEventsInformer(ctx context.Context) (cleanupFn func(), err error) {
	eventInformer := event.NewEventInformer(&pod.Tracker, pod.lastObject)
	eventInformer.WithChannels(pod.EventMsg, pod.objectFailed, pod.errors)
	return eventInformer.Run(ctx)
}

func (pod *Tracker) setupReadinessProbes() {
	for _, container := range pod.lastObject.Spec.Containers {
		if container.ReadinessProbe == nil || pod.readinessProbes[container.Name] != nil {
			continue
		}

		var ignoreReadinessProbeFailsByContainerName *time.Duration
		if ignore, ok := pod.ignoreReadinessProbeFailsByContainerName[container.Name]; ok {
			ignoreReadinessProbeFailsByContainerName = &ignore
		}

		var isStarted *bool
		for _, cs := range pod.LastStatus.ContainerStatuses {
			if cs.Name == container.Name {
				isStarted = cs.Started
				break
			}
		}

		readinessProbe := NewReadinessProbe(container.ReadinessProbe, container.StartupProbe, isStarted, ignoreReadinessProbeFailsByContainerName)
		pod.readinessProbes[container.Name] = &readinessProbe
	}
}

func (pod *Tracker) updateReadinessProbes() {
	for _, container := range pod.lastObject.Spec.Containers {
		if container.ReadinessProbe == nil {
			continue
		}

		var isStarted *bool
		for _, cs := range pod.LastStatus.ContainerStatuses {
			if cs.Name == container.Name {
				isStarted = cs.Started
				break
			}
		}

		readinessProbe, ok := pod.readinessProbes[container.Name]
		if !ok {
			panic("readinessProbe can't be unset while trying to update")
		}
		readinessProbe.SetupStartedAtTime(isStarted)
	}
}
