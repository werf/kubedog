package tracker

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"

	"github.com/flant/kubedog/pkg/display"
)

type PodFeed interface {
	Added() error
	Succeeded() error
	Failed(reason string) error
	EventMsg(msg string) error
	Ready() error
	ContainerLogChunk(*ContainerLogChunk) error
	ContainerError(ContainerError) error
}

type ContainerLogChunk struct {
	ContainerName string
	LogLines      []display.LogLine
}

type ContainerError struct {
	Message       string
	ContainerName string
}

func TrackPod(name, namespace string, kube kubernetes.Interface, feed PodFeed, opts Options) error {
	errorChan := make(chan error, 0)
	doneChan := make(chan struct{}, 0)

	parentContext := opts.ParentContext
	if parentContext == nil {
		parentContext = context.Background()
	}
	ctx, cancel := watchtools.ContextWithOptionalTimeout(parentContext, opts.Timeout)
	defer cancel()

	pod := NewPodTracker(ctx, name, namespace, kube)

	go func() {
		err := pod.Track()
		if err != nil {
			errorChan <- err
		} else {
			doneChan <- struct{}{}
		}
	}()

	for {
		select {
		case chunk := <-pod.ContainerLogChunk:
			if debug() {
				fmt.Printf("Pod `%s` container `%s` log chunk:\n", pod.ResourceName, chunk.ContainerName)
				for _, line := range chunk.LogLines {
					fmt.Printf("[%s] %s\n", line.Timestamp, line.Message)
				}
			}

			err := feed.ContainerLogChunk(chunk)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case containerError := <-pod.ContainerError:
			if debug() {
				fmt.Printf("Pod's `%s` container error: %#v", pod.ResourceName, containerError)
			}

			err := feed.ContainerError(containerError)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case <-pod.Added:
			if debug() {
				fmt.Printf("Pod `%s` added\n", pod.ResourceName)
			}

			err := feed.Added()
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case <-pod.Succeeded:
			if debug() {
				fmt.Printf("Pod `%s` succeeded\n", pod.ResourceName)
			}

			err := feed.Succeeded()
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case reason := <-pod.Failed:
			if debug() {
				fmt.Printf("Pod `%s` failed: %s\n", pod.ResourceName, reason)
			}

			err := feed.Failed(reason)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case msg := <-pod.EventMsg:
			if debug() {
				fmt.Printf("Pod `%s` event msg: %s\n", pod.ResourceName, msg)
			}

			err := feed.EventMsg(msg)
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case <-pod.Ready:
			if debug() {
				fmt.Printf("Pod `%s` ready\n", pod.ResourceName)
			}

			err := feed.Ready()
			if err == StopTrack {
				return nil
			}
			if err != nil {
				return err
			}

		case err := <-errorChan:
			return err

		case <-doneChan:
			return nil
		}
	}
}

type PodTracker struct {
	Tracker

	Added             chan struct{}
	Succeeded         chan struct{}
	Failed            chan string
	EventMsg          chan string
	Ready             chan struct{}
	ContainerLogChunk chan *ContainerLogChunk
	ContainerError    chan ContainerError

	State                           TrackerState
	ContainerTrackerStates          map[string]TrackerState
	ProcessedContainerLogTimestamps map[string]time.Time
	TrackedContainers               []string
	LogsFromTime                    time.Time

	lastObject     *corev1.Pod
	objectAdded    chan *corev1.Pod
	objectModified chan *corev1.Pod
	objectDeleted  chan *corev1.Pod
	objectFailed   chan string
	containerDone  chan string
	errors         chan error
}

func NewPodTracker(ctx context.Context, name, namespace string, kube kubernetes.Interface) *PodTracker {
	return &PodTracker{
		Tracker: Tracker{
			Kube:             kube,
			Namespace:        namespace,
			FullResourceName: fmt.Sprintf("po/%s", name),
			ResourceName:     name,
			Context:          ctx,
		},

		Added:             make(chan struct{}, 0),
		Succeeded:         make(chan struct{}, 0),
		Failed:            make(chan string, 1),
		EventMsg:          make(chan string, 1),
		Ready:             make(chan struct{}, 0),
		ContainerError:    make(chan ContainerError, 0),
		ContainerLogChunk: make(chan *ContainerLogChunk, 1000),

		State:                           Initial,
		ContainerTrackerStates:          make(map[string]TrackerState),
		ProcessedContainerLogTimestamps: make(map[string]time.Time),
		TrackedContainers:               make([]string, 0),
		LogsFromTime:                    time.Time{},

		objectAdded:    make(chan *corev1.Pod, 0),
		objectModified: make(chan *corev1.Pod, 0),
		objectDeleted:  make(chan *corev1.Pod, 0),
		objectFailed:   make(chan string, 1),
		errors:         make(chan error, 0),
		containerDone:  make(chan string, 10),
	}
}

func (pod *PodTracker) Track() error {
	err := pod.runInformer()
	if err != nil {
		return err
	}

	for {
		select {
		case containerName := <-pod.containerDone:
			trackedContainers := make([]string, 0)
			for _, name := range pod.TrackedContainers {
				if name != containerName {
					trackedContainers = append(trackedContainers, name)
				}
			}
			pod.TrackedContainers = trackedContainers

			done, err := pod.handlePodState(pod.lastObject)
			if err != nil {
				return err
			}
			if done {
				return nil
			}

		case object := <-pod.objectAdded:
			pod.lastObject = object
			pod.runEventsInformer()

			switch pod.State {
			case Initial:
				pod.State = ResourceAdded
				pod.Added <- struct{}{}

				err := pod.runContainersTrackers()
				if err != nil {
					return err
				}
			}

			done, err := pod.handlePodState(object)
			if err != nil {
				return err
			}
			if done {
				return nil
			}

		case object := <-pod.objectModified:
			pod.lastObject = object

			done, err := pod.handlePodState(object)
			if err != nil {
				return err
			}
			if done {
				return nil
			}

		case <-pod.objectDeleted:
			keys := []string{}
			for k := range pod.ContainerTrackerStates {
				keys = append(keys, k)
			}
			for _, k := range keys {
				pod.ContainerTrackerStates[k] = ContainerTrackerDone
			}

			if debug() {
				fmt.Printf("Pod `%s` resource gone: stop tracking\n", pod.ResourceName)
			}

			return nil

		case reason := <-pod.objectFailed:
			pod.State = "Failed"
			pod.Failed <- reason

		case <-pod.Context.Done():
			return ErrTrackTimeout

		case err := <-pod.errors:
			return err
		}
	}
}

func (pod *PodTracker) handlePodState(object *corev1.Pod) (done bool, err error) {
	err = pod.handleContainersState(object)
	if err != nil {
		return false, err
	}

	for _, cond := range object.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			pod.Ready <- struct{}{}
		}
	}

	if len(pod.TrackedContainers) == 0 {
		if object.Status.Phase == corev1.PodSucceeded {
			pod.Succeeded <- struct{}{}
			done = true
		} else if object.Status.Phase == corev1.PodFailed {
			pod.Failed <- "pod is in a Failed phase"
			done = true
		}
	}

	return
}

func (pod *PodTracker) handleContainersState(object *corev1.Pod) error {
	allContainerStatuses := make([]corev1.ContainerStatus, 0)
	for _, cs := range object.Status.InitContainerStatuses {
		allContainerStatuses = append(allContainerStatuses, cs)
	}
	for _, cs := range object.Status.ContainerStatuses {
		allContainerStatuses = append(allContainerStatuses, cs)
	}

	for _, cs := range allContainerStatuses {
		oldState := pod.ContainerTrackerStates[cs.Name]

		if cs.State.Waiting != nil {
			switch cs.State.Waiting.Reason {
			case "ImagePullBackOff", "ErrImagePull", "CrashLoopBackOff":
				pod.ContainerError <- ContainerError{
					ContainerName: cs.Name,
					Message:       fmt.Sprintf("%s: %s", cs.State.Waiting.Reason, cs.State.Waiting.Message),
				}
			}
		}

		if cs.State.Running != nil || cs.State.Terminated != nil {
			pod.ContainerTrackerStates[cs.Name] = FollowingContainerLogs
		}

		if oldState != pod.ContainerTrackerStates[cs.Name] {
			if debug() {
				fmt.Printf("Pod `%s` container `%s` state changed %#v -> %#v\n", pod.ResourceName, cs.Name, oldState, pod.ContainerTrackerStates[cs.Name])
			}
		}
	}

	return nil
}

func (pod *PodTracker) followContainerLogs(containerName string) error {
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
	req := pod.Kube.Core().
		Pods(pod.Namespace).
		GetLogs(pod.ResourceName, logOpts)

	readCloser, err := req.Stream()
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
		case <-pod.Context.Done():
			return ErrTrackTimeout
		default:
		}
	}

	return nil
}

func (pod *PodTracker) trackContainer(containerName string) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			state := pod.ContainerTrackerStates[containerName]

			switch state {
			case FollowingContainerLogs:
				err := pod.followContainerLogs(containerName)
				if err != nil {
					if debug() {
						fmt.Fprintf(os.Stderr, "Pod `%s` Container `%s` logs streaming error: %s\n", pod.ResourceName, containerName, err)
					}
				}
				return nil
			case Initial:
			case ContainerTrackerDone:
				return nil
			default:
				return fmt.Errorf("unknown Pod's `%s` Container `%s` tracker state `%s`", pod.ResourceName, containerName, state)
			}

		case <-pod.Context.Done():
			return ErrTrackTimeout
		}
	}
}

func (pod *PodTracker) runContainersTrackers() error {
	podManifest, err := pod.Kube.Core().
		Pods(pod.Namespace).
		Get(pod.ResourceName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	allContainersNames := make([]string, 0)
	for _, containerConf := range podManifest.Spec.InitContainers {
		allContainersNames = append(allContainersNames, containerConf.Name)
	}
	for _, containerConf := range podManifest.Spec.Containers {
		allContainersNames = append(allContainersNames, containerConf.Name)
	}
	for i := range allContainersNames {
		containerName := allContainersNames[i]

		pod.ContainerTrackerStates[containerName] = Initial
		pod.TrackedContainers = append(pod.TrackedContainers, containerName)

		go func() {
			if debug() {
				fmt.Printf("Starting to track Pod's `%s` container `%s`\n", pod.ResourceName, containerName)
			}

			err := pod.trackContainer(containerName)
			if err != nil {
				pod.errors <- err
			}

			if debug() {
				fmt.Printf("Done tracking Pod's `%s` container `%s`\n", pod.ResourceName, containerName)
			}

			pod.containerDone <- containerName
		}()
	}

	return nil
}

func (pod *PodTracker) runInformer() error {
	tweakListOptions := func(options metav1.ListOptions) metav1.ListOptions {
		options.FieldSelector = fields.OneTermEqualSelector("metadata.name", pod.ResourceName).String()
		return options
	}
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return pod.Kube.Core().Pods(pod.Namespace).List(tweakListOptions(options))
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return pod.Kube.Core().Pods(pod.Namespace).Watch(tweakListOptions(options))
		},
	}

	go func() {
		_, err := watchtools.UntilWithSync(pod.Context, lw, &corev1.Pod{}, nil, func(e watch.Event) (bool, error) {
			if debug() {
				fmt.Printf("Pod `%s` informer event: %#v\n", pod.ResourceName, e.Type)
			}

			var object *corev1.Pod

			if e.Type != watch.Error {
				var ok bool
				object, ok = e.Object.(*corev1.Pod)
				if !ok {
					return true, fmt.Errorf("TRACK POD EVENT %s expect *corev1.Pod object, got %T", pod.ResourceName, e.Object)
				}
			}

			if e.Type == watch.Added {
				pod.objectAdded <- object
			} else if e.Type == watch.Modified {
				pod.objectModified <- object
			} else if e.Type == watch.Deleted {
				pod.objectDeleted <- object
			} else if e.Type == watch.Error {
				pod.errors <- fmt.Errorf("Pod %s error: %v", e.Object)
			}

			return false, nil
		})

		if err != nil {
			pod.errors <- err
		}

		if debug() {
			fmt.Printf("Pod `%s` informer done\n", pod.ResourceName)
		}
	}()

	return nil
}

// runEventsInformer watch for DaemonSet events
func (pod *PodTracker) runEventsInformer() {
	if pod.lastObject == nil {
		return
	}

	eventInformer := NewEventInformer(pod.Tracker, pod.lastObject)
	eventInformer.WithChannels(pod.EventMsg, pod.objectFailed, pod.errors)
	eventInformer.Run()

	return
}
