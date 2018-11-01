package monitor

import (
	"context"
	"fmt"
	"io"
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
)

type PodFeed interface {
	Succeeded() error
	Failed() error
	ContainerLogChunk(*ContainerLogChunk) error
	ContainerError(ContainerError) error
}

type LogLine struct {
	Timestamp string
	Data      string
}

type ContainerLogChunk struct {
	ContainerName string
	LogLines      []LogLine
}

type ContainerError struct {
	Message       string
	ContainerName string
}

func MonitorPod(name, namespace string, kube kubernetes.Interface, feed PodFeed, opts WatchOptions) error {
	errorChan := make(chan error, 0)
	doneChan := make(chan struct{}, 0)

	parentContext := opts.ParentContext
	if parentContext == nil {
		parentContext = context.Background()
	}
	ctx, cancel := watchtools.ContextWithOptionalTimeout(parentContext, opts.Timeout)
	defer cancel()

	pod := NewPodWatchMonitor(ctx, name, namespace, kube)

	go func() {
		err := pod.Watch()
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
					fmt.Printf("[%s] %s\n", line.Timestamp, line.Data)
				}
			}

			err := feed.ContainerLogChunk(chunk)
			if err == StopWatch {
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
			if err != nil {
				return err
			}
			if err == StopWatch {
				return nil
			}

		case <-pod.Succeeded:
			if debug() {
				fmt.Printf("Pod `%s` succeeded\n", pod.ResourceName)
			}

			err := feed.Succeeded()
			if err != nil {
				return err
			}
			if err == StopWatch {
				return nil
			}

		case <-pod.Failed:
			if debug() {
				fmt.Printf("Pod `%s` failed\n", pod.ResourceName)
			}

			err := feed.Failed()
			if err != nil {
				return err
			}
			if err == StopWatch {
				return nil
			}

		case err := <-errorChan:
			return err

		case <-doneChan:
			return nil
		}
	}
}

type PodWatchMonitor struct {
	WatchMonitor

	Succeeded         chan bool
	Failed            chan bool
	ContainerLogChunk chan *ContainerLogChunk
	ContainerError    chan ContainerError

	State                           WatchMonitorState
	ContainerMonitorStates          map[string]WatchMonitorState
	ProcessedContainerLogTimestamps map[string]time.Time
	MonitoredContainers             []string

	lastObject    *corev1.Pod
	objectUpdated chan *corev1.Pod
	errors        chan error
	containerDone chan string
}

func NewPodWatchMonitor(ctx context.Context, name, namespace string, kube kubernetes.Interface) *PodWatchMonitor {
	return &PodWatchMonitor{
		WatchMonitor: WatchMonitor{
			Kube:         kube,
			Namespace:    namespace,
			ResourceName: name,
			Context:      ctx,
		},

		Succeeded:         make(chan bool, 0),
		Failed:            make(chan bool, 0),
		ContainerError:    make(chan ContainerError, 0),
		ContainerLogChunk: make(chan *ContainerLogChunk, 1000),

		State: WatchInitial,
		ContainerMonitorStates:          make(map[string]WatchMonitorState),
		ProcessedContainerLogTimestamps: make(map[string]time.Time),
		MonitoredContainers:             make([]string, 0),

		objectUpdated: make(chan *corev1.Pod, 0),
		errors:        make(chan error, 0),
		containerDone: make(chan string, 10),
	}
}

func (pod *PodWatchMonitor) Watch() error {
	err := pod.runContainersWatchers()
	if err != nil {
		return err
	}

	err = pod.runInformer()
	if err != nil {
		return err
	}

	for {
		select {
		case containerName := <-pod.containerDone:
			monitoredContainers := make([]string, 0)
			for _, name := range pod.MonitoredContainers {
				if name != containerName {
					monitoredContainers = append(monitoredContainers, name)
				}
			}
			pod.MonitoredContainers = monitoredContainers

			done, err := pod.handlePodState(pod.lastObject)
			if err != nil {
				return err
			}
			if done {
				return nil
			}

		case object := <-pod.objectUpdated:
			pod.lastObject = object

			allContainerStatuses := make([]corev1.ContainerStatus, 0)
			for _, cs := range object.Status.InitContainerStatuses {
				allContainerStatuses = append(allContainerStatuses, cs)
			}
			for _, cs := range object.Status.ContainerStatuses {
				allContainerStatuses = append(allContainerStatuses, cs)
			}

			for _, cs := range allContainerStatuses {
				oldState := pod.ContainerMonitorStates[cs.Name]

				if cs.State.Waiting != nil {
					pod.ContainerMonitorStates[cs.Name] = ContainerWaiting

					switch cs.State.Waiting.Reason {
					case "ImagePullBackOff", "ErrImagePull", "CrashLoopBackOff": // FIXME: change to constants
						pod.ContainerError <- ContainerError{
							ContainerName: cs.Name,
							Message:       fmt.Sprintf("%s: %s", cs.State.Waiting.Reason, cs.State.Waiting.Message),
						}
					}
				}
				if cs.State.Running != nil {
					pod.ContainerMonitorStates[cs.Name] = ContainerRunning
				}
				if cs.State.Terminated != nil {
					pod.ContainerMonitorStates[cs.Name] = ContainerTerminated
				}

				if oldState != pod.ContainerMonitorStates[cs.Name] {
					if debug() {
						fmt.Printf("Pod `%s` container `%s` state changed %#v -> %#v\n", pod.ResourceName, cs.Name, oldState, pod.ContainerMonitorStates[cs.Name])
					}
				}
			}

			done, err := pod.handlePodState(object)
			if err != nil {
				return err
			}
			if done {
				return nil
			}

		case <-pod.Context.Done():
			return ErrWatchTimeout

		case err := <-pod.errors:
			return err
		}
	}
}

func (pod *PodWatchMonitor) handlePodState(object *corev1.Pod) (done bool, err error) {
	if len(pod.MonitoredContainers) == 0 {
		if object.Status.Phase == corev1.PodSucceeded {
			pod.Succeeded <- true
			done = true
		} else if object.Status.Phase == corev1.PodFailed {
			pod.Failed <- true
			done = true
		}
	}

	return
}

func (pod *PodWatchMonitor) followContainerLogs(containerName string) error {
	req := pod.Kube.Core().
		Pods(pod.Namespace).
		GetLogs(pod.ResourceName, &corev1.PodLogOptions{
			Container:  containerName,
			Timestamps: true,
			Follow:     true,
		})

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
			chunkLines := make([]LogLine, 0)
			for i := 0; i < n; i++ {
				bt := chunkBuf[i]

				if bt == '\n' {
					line := string(lineBuf)
					lineBuf = lineBuf[:0]

					lineParts := strings.SplitN(line, " ", 2)
					if len(lineParts) == 2 {
						chunkLines = append(chunkLines, LogLine{Timestamp: lineParts[0], Data: lineParts[1]})
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
			return ErrWatchTimeout
		default:
		}
	}

	return nil
}

func (pod *PodWatchMonitor) watchContainer(containerName string) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			state := pod.ContainerMonitorStates[containerName]

			switch state {
			case ContainerRunning, ContainerTerminated:
				return pod.followContainerLogs(containerName)
			case WatchInitial, ContainerWaiting:
			default:
				return fmt.Errorf("unknown Pod's `%s` Container `%s` watch state `%s`", pod.ResourceName, containerName, state)
			}

		case <-pod.Context.Done():
			return ErrWatchTimeout
		}
	}
}

func (pod *PodWatchMonitor) runContainersWatchers() error {
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

		pod.ContainerMonitorStates[containerName] = WatchInitial
		pod.MonitoredContainers = append(pod.MonitoredContainers, containerName)

		go func() {
			if debug() {
				fmt.Printf("Starting to watch Pod's `%s` container `%s`\n", pod.ResourceName, containerName)
			}

			err := pod.watchContainer(containerName)
			if err != nil {
				pod.errors <- err
			}

			if debug() {
				fmt.Printf("Done watch Pod's `%s` container `%s`\n", pod.ResourceName, containerName)
			}

			pod.containerDone <- containerName
		}()
	}

	return nil
}

func (pod *PodWatchMonitor) runInformer() error {
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
			// FIXME: handle DELETE event

			if debug() {
				fmt.Printf("Pod `%s` informer event: %#v\n", pod.ResourceName, e.Type)
			}

			object, ok := e.Object.(*corev1.Pod)
			if !ok {
				return true, fmt.Errorf("expected %s to be a *corev1.Pod, got %T", pod.ResourceName, e.Object)
			}

			pod.objectUpdated <- object

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
