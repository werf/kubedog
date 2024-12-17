package pod

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/werf/kubedog-for-werf-helm/pkg/tracker/indicators"
	"github.com/werf/kubedog-for-werf-helm/pkg/utils"
)

type PodStatus struct {
	corev1.PodStatus

	Name string

	StatusGeneration uint64

	StatusIndicator *indicators.StringEqualConditionIndicator
	Age             string
	Restarts        int32
	ReadyContainers int32
	TotalContainers int32

	IsReady      bool
	IsFailed     bool
	IsSucceeded  bool
	FailedReason string

	ContainersErrors []ContainerError
}

func NewPodStatus(pod *corev1.Pod, statusGeneration uint64, trackedContainers []string, isTrackerFailed bool, trackerFailedReason string) PodStatus {
	res := PodStatus{
		PodStatus:        pod.Status,
		TotalContainers:  int32(len(pod.Spec.Containers)),
		Age:              utils.TranslateTimestampSince(pod.CreationTimestamp),
		StatusIndicator:  &indicators.StringEqualConditionIndicator{},
		StatusGeneration: statusGeneration,
		Name:             pod.Name,
	}

	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			res.IsReady = true
			break
		}
	}

	var restarts, readyContainers int32

	reason := string(pod.Status.Phase)
	if pod.Status.Reason != "" {
		reason = pod.Status.Reason
	}

	initializing := false
	for i := range pod.Status.InitContainerStatuses {
		container := pod.Status.InitContainerStatuses[i]
		restarts += container.RestartCount
		switch {
		case container.State.Terminated != nil && container.State.Terminated.ExitCode == 0:
			continue
		case container.State.Terminated != nil:
			// initialization is failed
			if len(container.State.Terminated.Reason) == 0 {
				if container.State.Terminated.Signal != 0 {
					reason = fmt.Sprintf("Init:Signal:%d", container.State.Terminated.Signal)
				} else {
					reason = fmt.Sprintf("Init:ExitCode:%d", container.State.Terminated.ExitCode)
				}
			} else {
				reason = "Init:" + container.State.Terminated.Reason
			}
			initializing = true
		case container.State.Waiting != nil && len(container.State.Waiting.Reason) > 0 && container.State.Waiting.Reason != "PodInitializing":
			reason = "Init:" + container.State.Waiting.Reason
			initializing = true
		default:
			reason = fmt.Sprintf("Init:%d/%d", i, len(pod.Spec.InitContainers))
			initializing = true
		}
		break
	}

	if !initializing {
		restarts = 0
		hasRunning := false
		for i := len(pod.Status.ContainerStatuses) - 1; i >= 0; i-- {
			container := pod.Status.ContainerStatuses[i]

			restarts += container.RestartCount
			switch {
			case container.State.Waiting != nil && container.State.Waiting.Reason != "":
				reason = container.State.Waiting.Reason
			case container.State.Terminated != nil && container.State.Terminated.Reason != "":
				reason = container.State.Terminated.Reason
			case container.State.Terminated != nil && container.State.Terminated.Reason == "":
				if container.State.Terminated.Signal != 0 {
					reason = fmt.Sprintf("Signal:%d", container.State.Terminated.Signal)
				} else {
					reason = fmt.Sprintf("ExitCode:%d", container.State.Terminated.ExitCode)
				}
			case container.Ready && container.State.Running != nil:
				hasRunning = true
				readyContainers++
			}
		}

		// change pod status back to "Running" if there is at least one container still reporting as "Running" status
		if reason == "Completed" && hasRunning {
			reason = "Running"
		}
	}

	if pod.DeletionTimestamp != nil && pod.Status.Reason == "NodeLost" {
		reason = "Unknown"
	} else if pod.DeletionTimestamp != nil {
		reason = "Terminating"
	}

	res.StatusIndicator.Value = reason
	res.StatusIndicator.FailedValue = "Error"
	res.Restarts = restarts
	res.ReadyContainers = readyContainers

	if len(trackedContainers) == 0 {
		switch pod.Status.Phase {
		case corev1.PodSucceeded:
			res.IsSucceeded = true
		case corev1.PodFailed:
			res.IsFailed = true
			res.FailedReason = reason
		}
	}

	if !res.IsReady && !res.IsFailed && !res.IsSucceeded {
		res.IsFailed = isTrackerFailed
		res.FailedReason = trackerFailedReason
	}

	setContainersStatusesToPodStatus(&res, pod)

	return res
}

func setContainersStatusesToPodStatus(status *PodStatus, pod *corev1.Pod) {
	allContainerStatuses := make([]corev1.ContainerStatus, 0)
	allContainerStatuses = append(allContainerStatuses, pod.Status.InitContainerStatuses...)
	allContainerStatuses = append(allContainerStatuses, pod.Status.ContainerStatuses...)

	for _, cs := range allContainerStatuses {
		if cs.State.Waiting != nil {
			switch cs.State.Waiting.Reason {
			case "ImagePullBackOff", "ErrImagePull", "CrashLoopBackOff", "ErrImageNeverPull":
				if status.ContainersErrors == nil {
					status.ContainersErrors = []ContainerError{}
				}

				status.ContainersErrors = append(status.ContainersErrors, ContainerError{
					ContainerName: cs.Name,
					Message:       fmt.Sprintf("%s: %s", cs.State.Waiting.Reason, cs.State.Waiting.Message),
				})
			}
		}
	}
}
