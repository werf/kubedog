package multitrack

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/fatih/color"

	"github.com/flant/kubedog/pkg/tracker/indicators"
	"github.com/flant/kubedog/pkg/tracker/pod"
	"github.com/flant/kubedog/pkg/utils"
	"github.com/flant/logboek"
)

func (mt *multitracker) displayResourceLogChunk(resourceKind, resourceName string, header string, spec MultitrackSpec, chunk *pod.ContainerLogChunk) {
	for _, containerName := range spec.SkipLogsForContainers {
		if containerName == chunk.ContainerName {
			return
		}
	}

	showLogs := len(spec.ShowLogsOnlyForContainers) == 0
	for _, containerName := range spec.ShowLogsOnlyForContainers {
		if containerName == chunk.ContainerName {
			showLogs = true
		}
	}

	if !showLogs {
		return
	}

	var logRegexp *regexp.Regexp
	if spec.LogRegexByContainerName[chunk.ContainerName] != nil {
		logRegexp = spec.LogRegexByContainerName[chunk.ContainerName]
	} else if spec.LogRegex != nil {
		logRegexp = spec.LogRegex
	}

	showLines := []string{}

	if logRegexp != nil {
		for _, logLine := range chunk.LogLines {
			message := logRegexp.FindString(logLine.Message)
			if message != "" {
				showLines = append(showLines, logLine.Message)
			}
		}
	} else {
		for _, logLine := range chunk.LogLines {
			showLines = append(showLines, logLine.Message)
		}
	}

	if len(showLines) > 0 {
		mt.setLogProcess(fmt.Sprintf("%s/%s %s logs", resourceKind, resourceName, header))

		for _, line := range showLines {
			logboek.OutF("%s\n", line)
		}
	}
}

func (mt *multitracker) setLogProcess(header string) {
	if mt.currentLogProcessHeader != header {
		mt.resetLogProcess()

		logboek.LogProcessStart(header, logboek.LogProcessStartOptions{})
		mt.currentLogProcessHeader = header
	}
}

// resetLogProcess should be called every time something is about to be displayed
func (mt *multitracker) resetLogProcess() {
	mt.displayCalled = true
	if mt.currentLogProcessHeader != "" {
		logboek.LogProcessEnd(logboek.LogProcessEndOptions{WithoutLogOptionalLn: true, WithoutElapsedTime: true})
		mt.currentLogProcessHeader = ""
	}
}

func (mt *multitracker) displayResourceTrackerMessageF(resourceKind, resourceName string, format string, a ...interface{}) {
	resource := fmt.Sprintf("%s/%s", resourceKind, resourceName)
	mt.debugDisplayMessagesByResource[resource] = append(mt.debugDisplayMessagesByResource[resource], fmt.Sprintf(fmt.Sprintf("%s: %s", resource, format), a...))
}

func (mt *multitracker) displayResourceEventF(resourceKind, resourceName string, format string, a ...interface{}) {
	resource := fmt.Sprintf("%s/%s", resourceKind, resourceName)
	mt.debugDisplayMessagesByResource[resource] = append(mt.debugDisplayMessagesByResource[resource], fmt.Sprintf(fmt.Sprintf("%s event: %s", resource, format), a...))
}

func (mt *multitracker) displayResourceErrorF(resourceKind, resourceName string, format string, a ...interface{}) {
	mt.resetLogProcess()
	resource := fmt.Sprintf("%s/%s", resourceKind, resourceName)
	logboek.LogErrorF(fmt.Sprintf("%s ERROR: %s", resource, format), a...)
}

func (mt *multitracker) displayFailedTrackingResourcesDebugMessages() {
	for name, state := range mt.TrackingDeployments {
		if state.Status != resourceFailed {
			continue
		}
		mt.displayResourceDebugMessages("deploy", name)
	}
	for name, state := range mt.TrackingStatefulSets {
		if state.Status != resourceFailed {
			continue
		}
		mt.displayResourceDebugMessages("sts", name)
	}
	for name, state := range mt.TrackingDaemonSets {
		if state.Status != resourceFailed {
			continue
		}
		mt.displayResourceDebugMessages("ds", name)
	}
	for name, state := range mt.TrackingJobs {
		if state.Status != resourceFailed {
			continue
		}
		mt.displayResourceDebugMessages("job", name)
	}
}

func (mt *multitracker) displayResourceDebugMessages(resourceKind, resourceName string) {
	lines := mt.debugDisplayMessagesByResource[fmt.Sprintf("%s/%s", resourceKind, resourceName)]

	if len(lines) > 0 {
		mt.resetLogProcess()

		logboek.LogOptionalLn()

		logboek.LogBlock(fmt.Sprintf("Failed resource %s/%s debug messages", resourceKind, resourceName), logboek.LogBlockOptions{WithoutLogOptionalLn: true}, func() {
			for _, line := range lines {
				logboek.LogInfoF("%s", line)
			}
		})

		logboek.LogOptionalLn()
	}
}

func (mt *multitracker) displayMultitrackServiceMessageF(format string, a ...interface{}) {
	mt.resetLogProcess()
	logboek.LogHighlightF(format, a...)
}

func (mt *multitracker) displayMultitrackErrorMessageF(format string, a ...interface{}) {
	mt.resetLogProcess()
	logboek.LogErrorF(format, a...)
}

func (mt *multitracker) displayStatusProgress() error {
	displayLn := false
	if mt.displayCalled {
		displayLn = true
	}

	mt.resetLogProcess()

	if displayLn {
		logboek.LogOptionalLn()
	}

	caption := color.New(color.Bold).Sprint("Status progress")

	logboek.LogBlock(caption, logboek.LogBlockOptions{WithoutLogOptionalLn: true}, func() {
		mt.displayDeploymentsStatusProgress()
		mt.displayDaemonSetsStatusProgress()
		mt.displayStatefulSetsStatusProgress()
		mt.displayJobsProgress()
	})

	logboek.LogOptionalLn()

	return nil
}

func (mt *multitracker) displayJobsProgress() {
	t := utils.NewTable(.5, .1, .1, .1, .1, .1)
	t.SetWidth(logboek.ContentWidth() - 1)
	t.Header("JOB", "ACTIVE", "SUCCEEDED", "FAILED", "DURATION", "AGE")

	resourcesNames := []string{}
	for name := range mt.JobsSpecs {
		resourcesNames = append(resourcesNames, name)
	}
	sort.Strings(resourcesNames)

	for _, name := range resourcesNames {
		prevStatus := mt.PrevJobsStatuses[name]
		status := mt.JobsStatuses[name]

		spec := mt.JobsSpecs[name]

		showProgress := (status.StatusGeneration > prevStatus.StatusGeneration)
		disableWarningColors := (spec.FailMode == IgnoreAndContinueDeployProcess)

		resource := formatResourceCaption(name, spec.FailMode, status.IsComplete, status.IsFailed, true)

		succeeded := "-"
		if status.SucceededIndicator != nil {
			succeeded = status.SucceededIndicator.FormatTableElem(prevStatus.SucceededIndicator, indicators.FormatTableElemOptions{
				ShowProgress:         showProgress,
				DisableWarningColors: disableWarningColors,
			})
		}

		if status.IsFailed {
			t.Row(resource, status.Active, succeeded, status.Failed, status.Duration, status.Age, formatResourceError(disableWarningColors, status.FailedReason))
		} else {
			t.Row(resource, status.Active, succeeded, status.Failed, status.Duration, status.Age)
		}

		if len(status.Pods) > 0 {
			newPodsNames := []string{}
			for podName := range status.Pods {
				newPodsNames = append(newPodsNames, podName)
			}

			st := mt.displayChildPodsStatusProgress(&t, prevStatus.Pods, status.Pods, newPodsNames, spec.FailMode, showProgress, disableWarningColors)

			extraMsg := ""
			if len(status.WaitingForMessages) > 0 {
				extraMsg += "---\n"
				extraMsg += color.New(color.FgBlue).Sprintf("Waiting for: %s", strings.Join(status.WaitingForMessages, ", "))
			}
			st.Commit(extraMsg)
		}

		mt.PrevJobsStatuses[name] = status
	}

	if len(resourcesNames) > 0 {
		_, _ = logboek.OutF(t.Render())
	}
}

func (mt *multitracker) displayStatefulSetsStatusProgress() {
	t := utils.NewTable(.7, .1, .1, .1)
	t.SetWidth(logboek.ContentWidth() - 1)
	t.Header("STATEFULSET", "REPLICAS", "READY", "UP-TO-DATE")

	resourcesNames := []string{}
	for name := range mt.StatefulSetsSpecs {
		resourcesNames = append(resourcesNames, name)
	}
	sort.Strings(resourcesNames)

	for _, name := range resourcesNames {
		prevStatus := mt.PrevStatefulSetsStatuses[name]
		status := mt.StatefulSetsStatuses[name]

		spec := mt.StatefulSetsSpecs[name]

		showProgress := (status.StatusGeneration > prevStatus.StatusGeneration)
		disableWarningColors := (spec.FailMode == IgnoreAndContinueDeployProcess)

		resource := formatResourceCaption(name, spec.FailMode, status.IsReady, status.IsFailed, true)

		replicas := "-"
		if status.ReplicasIndicator != nil {
			replicas = status.ReplicasIndicator.FormatTableElem(prevStatus.ReplicasIndicator, indicators.FormatTableElemOptions{
				ShowProgress:         showProgress,
				DisableWarningColors: disableWarningColors,
				WithTargetValue:      true,
			})
		}

		ready := "-"
		if status.ReadyIndicator != nil {
			ready = status.ReadyIndicator.FormatTableElem(prevStatus.ReadyIndicator, indicators.FormatTableElemOptions{
				ShowProgress:         showProgress,
				DisableWarningColors: disableWarningColors,
			})
		}

		uptodate := "-"
		if status.UpToDateIndicator != nil {
			uptodate = status.UpToDateIndicator.FormatTableElem(prevStatus.UpToDateIndicator, indicators.FormatTableElemOptions{
				ShowProgress:         showProgress,
				DisableWarningColors: disableWarningColors,
			})
		}

		if status.IsFailed {
			t.Row(resource, replicas, ready, uptodate, formatResourceError(disableWarningColors, status.FailedReason))
		} else {
			t.Row(resource, replicas, ready, uptodate)
		}

		if len(status.Pods) > 0 {
			st := mt.displayChildPodsStatusProgress(&t, prevStatus.Pods, status.Pods, status.NewPodsNames, spec.FailMode, showProgress, disableWarningColors)
			extraMsg := ""
			if len(status.WaitingForMessages) > 0 {
				extraMsg += "---\n"
				extraMsg += color.New(color.FgBlue).Sprintf("Waiting for: %s", strings.Join(status.WaitingForMessages, ", "))
			}
			st.Commit(extraMsg)
		}

		mt.PrevStatefulSetsStatuses[name] = status
	}

	if len(resourcesNames) > 0 {
		_, _ = logboek.OutF(t.Render())
	}
}

func (mt *multitracker) displayDaemonSetsStatusProgress() {
	t := utils.NewTable(.7, .1, .1, .1)
	t.SetWidth(logboek.ContentWidth() - 1)
	t.Header("DAEMONSET", "REPLICAS", "AVAILABLE", "UP-TO-DATE")

	resourcesNames := []string{}
	for name := range mt.DaemonSetsSpecs {
		resourcesNames = append(resourcesNames, name)
	}
	sort.Strings(resourcesNames)

	for _, name := range resourcesNames {
		prevStatus := mt.PrevDaemonSetsStatuses[name]
		status := mt.DaemonSetsStatuses[name]

		spec := mt.DaemonSetsSpecs[name]

		showProgress := (status.StatusGeneration > prevStatus.StatusGeneration)
		disableWarningColors := (spec.FailMode == IgnoreAndContinueDeployProcess)

		resource := formatResourceCaption(name, spec.FailMode, status.IsReady, status.IsFailed, true)

		replicas := "-"
		if status.ReplicasIndicator != nil {
			replicas = status.ReplicasIndicator.FormatTableElem(prevStatus.ReplicasIndicator, indicators.FormatTableElemOptions{
				ShowProgress:         showProgress,
				DisableWarningColors: disableWarningColors,
				WithTargetValue:      true,
			})
		}

		available := "-"
		if status.AvailableIndicator != nil {
			available = status.AvailableIndicator.FormatTableElem(prevStatus.AvailableIndicator, indicators.FormatTableElemOptions{
				ShowProgress:         showProgress,
				DisableWarningColors: disableWarningColors,
			})
		}

		uptodate := "-"
		if status.UpToDateIndicator != nil {
			uptodate = status.UpToDateIndicator.FormatTableElem(prevStatus.UpToDateIndicator, indicators.FormatTableElemOptions{
				ShowProgress:         showProgress,
				DisableWarningColors: disableWarningColors,
			})
		}

		if status.IsFailed {
			t.Row(resource, replicas, available, uptodate, formatResourceError(disableWarningColors, status.FailedReason))
		} else {
			t.Row(resource, replicas, available, uptodate)
		}

		if len(status.Pods) > 0 {
			st := mt.displayChildPodsStatusProgress(&t, prevStatus.Pods, status.Pods, status.NewPodsNames, spec.FailMode, showProgress, disableWarningColors)
			extraMsg := ""
			if len(status.WaitingForMessages) > 0 {
				extraMsg += "---\n"
				extraMsg += color.New(color.FgBlue).Sprintf("Waiting for: %s", strings.Join(status.WaitingForMessages, ", "))
			}
			st.Commit(extraMsg)
		}

		mt.PrevDaemonSetsStatuses[name] = status
	}

	if len(resourcesNames) > 0 {
		_, _ = logboek.OutF(t.Render())
	}
}

func (mt *multitracker) displayDeploymentsStatusProgress() {
	t := utils.NewTable(.7, .1, .1, .1)
	t.SetWidth(logboek.ContentWidth() - 1)
	t.Header("DEPLOYMENT", "REPLICAS", "AVAILABLE", "UP-TO-DATE")

	resourcesNames := []string{}
	for name := range mt.DeploymentsSpecs {
		resourcesNames = append(resourcesNames, name)
	}
	sort.Strings(resourcesNames)

	for _, name := range resourcesNames {
		prevStatus := mt.PrevDeploymentsStatuses[name]
		status := mt.DeploymentsStatuses[name]

		spec := mt.DeploymentsSpecs[name]

		showProgress := (status.StatusGeneration > prevStatus.StatusGeneration)
		disableWarningColors := (spec.FailMode == IgnoreAndContinueDeployProcess)

		resource := formatResourceCaption(name, spec.FailMode, status.IsReady, status.IsFailed, true)

		replicas := "-"
		if status.ReplicasIndicator != nil {
			replicas = status.ReplicasIndicator.FormatTableElem(prevStatus.ReplicasIndicator, indicators.FormatTableElemOptions{
				ShowProgress:         showProgress,
				DisableWarningColors: disableWarningColors,
				WithTargetValue:      true,
			})
		}

		available := "-"
		if status.AvailableIndicator != nil {
			available = status.AvailableIndicator.FormatTableElem(prevStatus.AvailableIndicator, indicators.FormatTableElemOptions{
				ShowProgress:         showProgress,
				DisableWarningColors: disableWarningColors,
			})
		}

		uptodate := "-"
		if status.UpToDateIndicator != nil {
			uptodate = status.UpToDateIndicator.FormatTableElem(prevStatus.UpToDateIndicator, indicators.FormatTableElemOptions{
				ShowProgress:         showProgress,
				DisableWarningColors: disableWarningColors,
			})
		}

		if status.IsFailed {
			t.Row(resource, replicas, available, uptodate, formatResourceError(disableWarningColors, status.FailedReason))
		} else {
			t.Row(resource, replicas, available, uptodate)
		}

		if len(status.Pods) > 0 {
			st := mt.displayChildPodsStatusProgress(&t, prevStatus.Pods, status.Pods, status.NewPodsNames, spec.FailMode, showProgress, disableWarningColors)
			extraMsg := ""
			if len(status.WaitingForMessages) > 0 {
				extraMsg += "---\n"
				extraMsg += color.New(color.FgBlue).Sprintf("Waiting for: %s", strings.Join(status.WaitingForMessages, ", "))
			}
			st.Commit(extraMsg)
		}

		mt.PrevDeploymentsStatuses[name] = status
	}

	if len(resourcesNames) > 0 {
		_, _ = logboek.OutF(t.Render())
	}
}

func (mt *multitracker) displayChildPodsStatusProgress(t *utils.Table, prevPods map[string]pod.PodStatus, pods map[string]pod.PodStatus, newPodsNames []string, failMode FailMode, showProgress, disableWarningColors bool) *utils.Table {
	st := t.SubTable(.3, .16, .2, .16, .16)
	st.Header("POD", "RDY", "STATUS", "RESTARTS", "AGE")

	podsNames := []string{}
	for podName := range pods {
		podsNames = append(podsNames, podName)
	}
	sort.Strings(podsNames)

	var podRows [][]interface{}

	for _, podName := range podsNames {
		var podRow []interface{}

		isPodNew := false
		for _, newPodName := range newPodsNames {
			if newPodName == podName {
				isPodNew = true
			}
		}

		prevPodStatus := prevPods[podName]
		podStatus := pods[podName]

		isReady := false
		if podStatus.StatusIndicator != nil {
			isReady = podStatus.StatusIndicator.IsReady()
		}

		resource := formatResourceCaption(strings.Join(strings.Split(podName, "-")[1:], "-"), failMode, isReady, podStatus.IsFailed, isPodNew)

		ready := fmt.Sprintf("%d/%d", podStatus.ReadyContainers, podStatus.TotalContainers)

		status := "-"
		if podStatus.StatusIndicator != nil {
			status = podStatus.StatusIndicator.FormatTableElem(prevPodStatus.StatusIndicator, indicators.FormatTableElemOptions{
				ShowProgress:         showProgress,
				DisableWarningColors: disableWarningColors,
				IsResourceNew:        isPodNew,
			})
		}

		podRow = append(podRow, resource, ready, status, podStatus.Restarts, podStatus.Age)
		if podStatus.IsFailed {
			podRow = append(podRow, formatResourceError(disableWarningColors, podStatus.FailedReason))
		}

		podRows = append(podRows, podRow)
	}

	st.Rows(podRows...)

	return &st
}

func formatResourceError(disableWarningColors bool, reason string) string {
	msg := fmt.Sprintf("error: %s", reason)
	if disableWarningColors {
		return msg
	}
	return color.New(color.FgRed).Sprintf("%s", msg)
}

func formatResourceCaption(resourceCaption string, resourceFailMode FailMode, isReady bool, isFailed bool, isNew bool) string {
	if !isNew {
		return resourceCaption
	}

	switch resourceFailMode {
	case FailWholeDeployProcessImmediately:
		if isReady {
			return color.New(color.FgGreen).Sprintf("%s", resourceCaption)
		} else if isFailed {
			return color.New(color.FgRed).Sprintf("%s", resourceCaption)
		} else {
			return color.New(color.FgYellow).Sprintf("%s", resourceCaption)
		}

	case IgnoreAndContinueDeployProcess:
		if isReady {
			return color.New(color.FgGreen).Sprintf("%s", resourceCaption)
		} else {
			return resourceCaption
		}

	case HopeUntilEndOfDeployProcess:
		if isReady {
			return color.New(color.FgGreen).Sprintf("%s", resourceCaption)
		} else {
			return color.New(color.FgYellow).Sprintf("%s", resourceCaption)
		}

	default:
		panic(fmt.Sprintf("unsupported resource fail mode '%s'", resourceFailMode))
	}
}

func podContainerLogChunkHeader(podName string, chunk *pod.ContainerLogChunk) string {
	return fmt.Sprintf("po/%s container/%s", podName, chunk.ContainerName)
}
