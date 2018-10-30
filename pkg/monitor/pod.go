package monitor

import (
	"bytes"
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
	// TODO:
	// Started() error
	// Failed(reason string) error
	ContainerLogChunk(*ContainerLogChunk) error
	PodError(PodError) error
}

type LogLine struct {
	Timestamp string
	Data      string
}

type ContainerLogChunk struct {
	ContainerName string
	LogLines      []LogLine
}

type PodError struct {
	Message       string
	PodName       string
	ContainerName string
}

func MonitorPod(name, namespace string, kube kubernetes.Interface, feed PodFeed, opts WatchOptions) error {
	errorChan := make(chan error, 0)

	pod := NewPodWatchMonitor(name, namespace, kube, opts)

	go func() {
		err := pod.Watch()
		if err != nil {
			errorChan <- err
		}
	}()

	// TODO: feed funcs special return value to stop monitor
	for {
		select {
		case chunk := <-pod.ContainerLogChunk:
			if debug() {
				fmt.Printf("Pod `%s` log chunk\n", pod.ResourceName)
			}

			err := feed.ContainerLogChunk(chunk)
			if err != nil {
				return err
			}

		case podError := <-pod.PodError:
			if debug() {
				fmt.Printf("Pod's `%s` error: %#v", pod.ResourceName, podError)
			}

			err := feed.PodError(podError)
			if err != nil {
				return err
			}

		case err := <-errorChan:
			return err
		}
	}
}

type PodWatchMonitor struct {
	WatchMonitor

	ContainerLogChunk chan *ContainerLogChunk
	PodError          chan PodError
	Error             chan error

	ContainerMonitorStates          map[string]string
	ProcessedContainerLogTimestamps map[string]time.Time
}

func NewPodWatchMonitor(name, namespace string, kube kubernetes.Interface, opts WatchOptions) *PodWatchMonitor {
	return &PodWatchMonitor{
		WatchMonitor: WatchMonitor{
			Kube:         kube,
			Timeout:      opts.Timeout,
			Namespace:    namespace,
			ResourceName: name,
		},
		PodError:                        make(chan PodError, 0),
		ContainerLogChunk:               make(chan *ContainerLogChunk, 1000),
		ContainerMonitorStates:          make(map[string]string),
		ProcessedContainerLogTimestamps: make(map[string]time.Time),
	}
}

func (pod *PodWatchMonitor) FollowContainerLogs(containerName string) error {
	client := pod.Kube

	req := client.Core().
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

	lineBuf := bytes.Buffer{}
	rawBuf := make([]byte, 4096)

	for {
		n, err := readCloser.Read(rawBuf)
		if err != nil && err == io.EOF {
			break
		} else if err != nil {
			return err
		}

		chunkLines := make([]LogLine, 0)
		for i := 0; i < n; i++ {
			if rawBuf[i] == '\n' {
				lineParts := strings.SplitN(lineBuf.String(), " ", 2)
				if len(lineParts) == 2 {
					chunkLines = append(chunkLines, LogLine{Timestamp: lineParts[0], Data: lineParts[1]})
				}

				lineBuf.Reset()
				continue
			}

			lineBuf.WriteByte(rawBuf[i])
		}

		pod.ContainerLogChunk <- &ContainerLogChunk{
			ContainerName: containerName,
			LogLines:      chunkLines,
		}
	}

	return nil
}

func (pod *PodWatchMonitor) WatchContainerLogs(containerName string) error {
	for {
		switch pod.ContainerMonitorStates[containerName] {
		case "Running", "Terminated":
			return pod.FollowContainerLogs(containerName)
		case "Waiting":
		default:
		}

		time.Sleep(time.Duration(200) * time.Millisecond)
	}
}

func (pod *PodWatchMonitor) Watch() error {
	client := pod.Kube
	informerChan := make(chan *corev1.Pod, 0)
	errorChan := make(chan error, 0)

	podManifest, err := client.Core().
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
		go func() {
			if debug() {
				fmt.Printf("Starting to watch Pod's `%s` container `%s`\n", pod.ResourceName, containerName)
			}

			err := pod.WatchContainerLogs(containerName)
			if err != nil {
				errorChan <- err
			} else {
				if debug() {
					fmt.Printf("Done watch pod's `%s` container `%s` logs\n", pod.ResourceName, containerName)
				}
			}
		}()
	}

	tweakListOptions := func(options metav1.ListOptions) metav1.ListOptions {
		options.FieldSelector = fields.OneTermEqualSelector("metadata.name", pod.ResourceName).String()
		return options
	}
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return client.Core().Pods(pod.Namespace).List(tweakListOptions(options))
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return client.Core().Pods(pod.Namespace).Watch(tweakListOptions(options))
		},
	}

	ctx, cancel := watchtools.ContextWithOptionalTimeout(context.Background(), 0)
	defer cancel()

	go func() {
		_, err := watchtools.UntilWithSync(ctx, lw, &corev1.Pod{}, nil, func(e watch.Event) (bool, error) {
			if debug() {
				fmt.Printf("Pod `%s` informer event: %#v\n", pod.ResourceName, e.Type)
			}

			object, ok := e.Object.(*corev1.Pod)
			if !ok {
				return true, fmt.Errorf("expected %s to be a *corev1.Pod, got %T", pod.ResourceName, e.Object)
			}

			informerChan <- object

			return false, nil
		})

		if err != nil {
			errorChan <- err
		} else {
			if debug() {
				fmt.Printf("Pod `%s` informer done\n")
			}
		}
	}()

	for {
		select {
		case object := <-informerChan:
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
					pod.ContainerMonitorStates[cs.Name] = "Waiting"

					switch cs.State.Waiting.Reason {
					case "ImagePullBackOff", "ErrImagePull", "CrashLoopBackOff":
						pod.PodError <- PodError{
							ContainerName: cs.Name,
							PodName:       pod.ResourceName,
							Message:       fmt.Sprintf("%s: %s", cs.State.Waiting.Reason, cs.State.Waiting.Message),
						}
					}
				}
				if cs.State.Running != nil {
					pod.ContainerMonitorStates[cs.Name] = "Running"
				}
				if cs.State.Terminated != nil {
					pod.ContainerMonitorStates[cs.Name] = "Terminated"
				}

				if oldState != pod.ContainerMonitorStates[cs.Name] {
					if debug() {
						fmt.Printf("Pod `%s` container `%s` state changed %#v -> %#v\n", pod.ResourceName, cs.Name, oldState, pod.ContainerMonitorStates[cs.Name])
					}
				}
			}

		case err := <-errorChan:
			return err
		}
	}
}
