package multitrack

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	dur "k8s.io/apimachinery/pkg/util/duration"

	"github.com/werf/kubedog/pkg/tracker/indicators"
	"github.com/werf/kubedog/pkg/tracker/pod"
	"github.com/werf/kubedog/pkg/trackers/rollout/multitrack/generic"
	"github.com/werf/kubedog/pkg/utils"
	"github.com/werf/logboek"
	"github.com/werf/logboek/pkg/style"
	"github.com/werf/logboek/pkg/types"
)

var (
	statusProgressTableRatio    = []float64{.58, .11, .12, .19}
	statusProgressSubTableRatio = []float64{.40, .15, .20, .25}
)

func (mt *multitracker) displayResourceLogChunk(resourceKind string, spec MultitrackSpec, header string, chunk *pod.ContainerLogChunk) {
	if spec.SkipLogs {
		return
	}

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
		mt.setLogProcess(fmt.Sprintf("%s/%s %s logs", resourceKind, spec.ResourceName, header), func(options types.LogProcessOptionsInterface) {
			options.WithoutElapsedTime()
		})

		for _, line := range showLines {
			logboek.Context(context.Background()).LogF("%s\n", line)
		}
	}
}

func (mt *multitracker) setLogProcess(header string, optionsFunc func(types.LogProcessOptionsInterface)) {
	if mt.currentLogProcessHeader != header {
		mt.resetLogProcess()

		logProcess := logboek.Context(context.Background()).Default().LogProcess(header)

		if optionsFunc != nil {
			logProcess.Options(optionsFunc)
		}

		logProcess.Start()

		mt.currentLogProcessHeader = header
		mt.currentLogProcess = logProcess
	}
}

func (mt *multitracker) resetLogProcess() {
	mt.displayCalled = true

	if mt.currentLogProcess != nil {
		mt.currentLogProcess.End()
		mt.currentLogProcess = nil
		mt.currentLogProcessHeader = ""
	}
}

func (mt *multitracker) displayResourceTrackerMessageF(resourceKind, resourceName string, showServiceMessages bool, format string, a ...interface{}) {
	resource := fmt.Sprintf("%s/%s", resourceKind, resourceName)
	msg := fmt.Sprintf(format, a...)
	mt.serviceMessagesByResource[resource] = append(mt.serviceMessagesByResource[resource], msg)

	if showServiceMessages {
		mt.setLogProcess(
			fmt.Sprintf("%s/%s service messages", resourceKind, resourceName),
			func(options types.LogProcessOptionsInterface) {
				options.Style(style.Details())
				options.WithoutElapsedTime()
			},
		)

		logboek.Context(context.Background()).Default().LogFDetails("%s\n", msg)
	}
}

func (mt *multitracker) displayResourceEventF(resourceKind, resourceName string, showServiceMessages bool, format string, a ...interface{}) {
	resource := fmt.Sprintf("%s/%s", resourceKind, resourceName)
	msg := fmt.Sprintf(fmt.Sprintf("event: %s", format), a...)
	mt.serviceMessagesByResource[resource] = append(mt.serviceMessagesByResource[resource], msg)

	if showServiceMessages {
		mt.setLogProcess(
			fmt.Sprintf("%s/%s service messages", resourceKind, resourceName),
			func(options types.LogProcessOptionsInterface) {
				options.Style(style.Details())
				options.WithoutElapsedTime()
			},
		)

		logboek.Context(context.Background()).Default().LogFDetails("%s\n", msg)
	}
}

func (mt *multitracker) displayResourceErrorF(resourceKind, resourceName, format string, a ...interface{}) {
	mt.resetLogProcess()
	logboek.Context(context.Background()).Warn().LogF(fmt.Sprintf("%s/%s ERROR: %s\n", resourceKind, resourceName, format), a...)
}

func (mt *multitracker) displayFailedTrackingResourcesServiceMessages() {
	for name, state := range mt.TrackingDeployments {
		if state.Status != resourceFailed {
			continue
		}

		spec := mt.DeploymentsSpecs[name]
		mt.displayResourceServiceMessages("deploy", spec.ResourceName)
	}
	for name, state := range mt.TrackingStatefulSets {
		if state.Status != resourceFailed {
			continue
		}

		spec := mt.StatefulSetsSpecs[name]
		mt.displayResourceServiceMessages("sts", spec.ResourceName)
	}
	for name, state := range mt.TrackingDaemonSets {
		if state.Status != resourceFailed {
			continue
		}

		spec := mt.DaemonSetsSpecs[name]
		mt.displayResourceServiceMessages("ds", spec.ResourceName)
	}
	for name, state := range mt.TrackingJobs {
		if state.Status != resourceFailed {
			continue
		}

		spec := mt.JobsSpecs[name]
		mt.displayResourceServiceMessages("job", spec.ResourceName)
	}

	for _, res := range mt.GenericResources {
		if res.State.ResourceState() != generic.ResourceStateFailed {
			continue
		}

		mt.displayResourceServiceMessages(res.Spec.GroupVersionKindNamespaceString(), res.Spec.Name)
	}
}

func (mt *multitracker) displayResourceServiceMessages(resourceKind, resourceName string) {
	lines := mt.serviceMessagesByResource[fmt.Sprintf("%s/%s", resourceKind, resourceName)]

	if len(lines) > 0 {
		mt.resetLogProcess()

		logboek.Context(context.Background()).LogOptionalLn()

		logboek.Context(context.Background()).Default().LogBlock("Failed resource %s/%s service messages", resourceKind, resourceName).
			Options(func(options types.LogBlockOptionsInterface) {
				options.WithoutLogOptionalLn()
				options.Style(style.Details())
			}).
			Do(func() {
				for _, line := range lines {
					logboek.Context(context.Background()).Default().LogFDetails("%s\n", line)
				}
			})

		logboek.Context(context.Background()).LogOptionalLn()
	}
}

func (mt *multitracker) displayMultitrackServiceMessageF(format string, a ...interface{}) {
	mt.resetLogProcess()
	logboek.Context(context.Background()).Default().LogFHighlight(format, a...)
}

func (mt *multitracker) displayMultitrackErrorMessageF(format string, a ...interface{}) {
	mt.resetLogProcess()
	logboek.Context(context.Background()).Warn().LogF(format, a...)
}

func (mt *multitracker) displayStatusProgress() error {
	displayLn := false
	if mt.displayCalled {
		displayLn = true
	}

	mt.resetLogProcess()

	if displayLn {
		logboek.Context(context.Background()).LogOptionalLn()
	}

	caption := utils.BoldF("Status progress")

	logboek.Context(context.Background()).Default().LogBlock(caption).
		Options(func(options types.LogBlockOptionsInterface) {
			options.WithoutLogOptionalLn()
		}).
		Do(func() {
			mt.displayDeploymentsStatusProgress()
			mt.displayDaemonSetsStatusProgress()
			mt.displayStatefulSetsStatusProgress()
			mt.displayJobsProgress()
			mt.displayCanariesProgress()
			mt.displayGenericsStatusProgress()
		})

	logboek.Context(context.Background()).LogOptionalLn()

	return nil
}

func (mt *multitracker) displayCanariesProgress() {
	t := utils.NewTable(statusProgressTableRatio...)
	t.SetWidth(logboek.Context(context.Background()).Streams().ContentWidth() - 1)
	t.Header("CANARY", "STATUS", "WEIGHT", "LASTUPDATE")

	resourcesNames := []string{}
	for name := range mt.CanariesSpecs {
		resourcesNames = append(resourcesNames, name)
	}
	sort.Strings(resourcesNames)

	var tableChangesCount int
	for _, name := range resourcesNames {
		status := mt.CanariesStatuses[name]

		spec := mt.CanariesSpecs[name]
		resource := formatResourceCaption(name, spec.FailMode, status.IsSucceeded, status.IsFailed, true)

		if status.IsFailed {
			tableChangesCount++
			t.Row(resource, status.FailedReason, status.CanaryWeight, status.LastTransitionTime)
		} else {
			tableChangesCount++
			t.Row(resource, status.CanaryStatus.Phase, status.CanaryWeight, status.LastTransitionTime)
		}
	}

	if tableChangesCount > 0 {
		logboek.Context(context.Background()).Log(t.Render())
	}
}

func (mt *multitracker) displayJobsProgress() {
	t := utils.NewTable(statusProgressTableRatio...)
	t.SetWidth(logboek.Context(context.Background()).Streams().ContentWidth() - 1)
	t.Header("JOB", "ACTIVE", "DURATION", "SUCCEEDED/FAILED")

	resourcesNames := []string{}
	for name := range mt.JobsSpecs {
		resourcesNames = append(resourcesNames, name)
	}
	sort.Strings(resourcesNames)

	var tableChangesCount int
	for _, name := range resourcesNames {
		prevStatus := mt.PrevJobsStatuses[name]
		status := mt.JobsStatuses[name]

		spec := mt.JobsSpecs[name]

		if stillSucceeded := prevStatus.IsSucceeded && status.IsSucceeded; stillSucceeded {
			continue
		}

		showProgress := status.StatusGeneration > prevStatus.StatusGeneration
		disableWarningColors := spec.FailMode == IgnoreAndContinueDeployProcess

		resource := formatResourceCaption(name, spec.FailMode, status.IsSucceeded, status.IsFailed, true)

		succeeded := "-"
		if status.SucceededIndicator != nil {
			succeeded = status.SucceededIndicator.FormatTableElem(prevStatus.SucceededIndicator, indicators.FormatTableElemOptions{
				ShowProgress:         showProgress,
				DisableWarningColors: disableWarningColors,
			})
		}

		var duration string
		switch {
		case status.JobStatus.StartTime == nil:
		case status.JobStatus.CompletionTime == nil:
			duration = dur.HumanDuration(time.Since(status.JobStatus.StartTime.Time))
		default:
			duration = dur.HumanDuration(status.JobStatus.CompletionTime.Sub(status.JobStatus.StartTime.Time))
		}

		if status.IsFailed {
			tableChangesCount++
			t.Row(resource, status.Active, duration, strings.Join([]string{succeeded, fmt.Sprintf("%d", status.Failed)}, "/"), formatResourceError(disableWarningColors, status.FailedReason))
		} else {
			tableChangesCount++
			t.Row(resource, status.Active, duration, strings.Join([]string{succeeded, fmt.Sprintf("%d", status.Failed)}, "/"))
		}

		if len(status.Pods) > 0 {
			newPodsNames := []string{}
			for podName := range status.Pods {
				newPodsNames = append(newPodsNames, podName)
			}

			st, podTableChangesCount := mt.displayChildPodsStatusProgress(&t, prevStatus.Pods, status.Pods, newPodsNames, spec.FailMode, showProgress, disableWarningColors)
			tableChangesCount = tableChangesCount + podTableChangesCount

			extraMsg := ""
			if len(status.WaitingForMessages) > 0 {
				tableChangesCount++
				extraMsg += "---\n"
				extraMsg += utils.BlueF("Waiting for: %s", strings.Join(status.WaitingForMessages, ", "))
			}
			st.Commit(extraMsg)
		}

		mt.PrevJobsStatuses[name] = status
	}

	if tableChangesCount > 0 {
		logboek.Context(context.Background()).Log(t.Render())
	}
}

func (mt *multitracker) displayStatefulSetsStatusProgress() {
	t := utils.NewTable(statusProgressTableRatio...)
	t.SetWidth(logboek.Context(context.Background()).Streams().ContentWidth() - 1)
	t.Header("STATEFULSET", "REPLICAS", "READY", "UP-TO-DATE")

	resourcesNames := []string{}
	for name := range mt.StatefulSetsSpecs {
		resourcesNames = append(resourcesNames, name)
	}
	sort.Strings(resourcesNames)

	var tableChangesCount int
	for _, name := range resourcesNames {
		prevStatus := mt.PrevStatefulSetsStatuses[name]
		status := mt.StatefulSetsStatuses[name]

		spec := mt.StatefulSetsSpecs[name]

		if stillDeployed := prevStatus.IsReady && status.IsReady; stillDeployed {
			continue
		}

		showProgress := status.StatusGeneration > prevStatus.StatusGeneration
		disableWarningColors := spec.FailMode == IgnoreAndContinueDeployProcess

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
			tableChangesCount++
			t.Row(resource, replicas, ready, uptodate, formatResourceError(disableWarningColors, status.FailedReason))
		} else {
			args := []interface{}{}
			args = append(args, resource, replicas, ready, uptodate)
			for _, w := range status.WarningMessages {
				args = append(args, formatResourceWarning(disableWarningColors, w))
			}
			tableChangesCount++
			t.Row(args...)
		}

		if len(status.Pods) > 0 {
			st, podTableChangesCount := mt.displayChildPodsStatusProgress(&t, prevStatus.Pods, status.Pods, status.NewPodsNames, spec.FailMode, showProgress, disableWarningColors)
			tableChangesCount = tableChangesCount + podTableChangesCount
			extraMsg := ""
			if len(status.WaitingForMessages) > 0 {
				tableChangesCount++
				extraMsg += "---\n"
				extraMsg += utils.BlueF("Waiting for: %s", strings.Join(status.WaitingForMessages, ", "))
			}
			st.Commit(extraMsg)
		}

		mt.PrevStatefulSetsStatuses[name] = status
	}

	if tableChangesCount > 0 {
		logboek.Context(context.Background()).Log(t.Render())
	}
}

func (mt *multitracker) displayDaemonSetsStatusProgress() {
	t := utils.NewTable(statusProgressTableRatio...)
	t.SetWidth(logboek.Context(context.Background()).Streams().ContentWidth() - 1)
	t.Header("DAEMONSET", "REPLICAS", "AVAILABLE", "UP-TO-DATE")

	resourcesNames := []string{}
	for name := range mt.DaemonSetsSpecs {
		resourcesNames = append(resourcesNames, name)
	}
	sort.Strings(resourcesNames)

	var tableChangesCount int
	for _, name := range resourcesNames {
		prevStatus := mt.PrevDaemonSetsStatuses[name]
		status := mt.DaemonSetsStatuses[name]

		spec := mt.DaemonSetsSpecs[name]

		if stillDeployed := prevStatus.IsReady && status.IsReady; stillDeployed {
			continue
		}

		showProgress := status.StatusGeneration > prevStatus.StatusGeneration
		disableWarningColors := spec.FailMode == IgnoreAndContinueDeployProcess

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
			tableChangesCount++
			t.Row(resource, replicas, available, uptodate, formatResourceError(disableWarningColors, status.FailedReason))
		} else {
			tableChangesCount++
			t.Row(resource, replicas, available, uptodate)
		}

		if len(status.Pods) > 0 {
			st, podTableChangesCount := mt.displayChildPodsStatusProgress(&t, prevStatus.Pods, status.Pods, status.NewPodsNames, spec.FailMode, showProgress, disableWarningColors)
			tableChangesCount = tableChangesCount + podTableChangesCount
			extraMsg := ""
			if len(status.WaitingForMessages) > 0 {
				tableChangesCount++
				extraMsg += "---\n"
				extraMsg += utils.BlueF("Waiting for: %s", strings.Join(status.WaitingForMessages, ", "))
			}
			st.Commit(extraMsg)
		}

		mt.PrevDaemonSetsStatuses[name] = status
	}

	if tableChangesCount > 0 {
		logboek.Context(context.Background()).Log(t.Render())
	}
}

func (mt *multitracker) displayDeploymentsStatusProgress() {
	t := utils.NewTable(statusProgressTableRatio...)
	t.SetWidth(logboek.Context(context.Background()).Streams().ContentWidth() - 1)
	t.Header("DEPLOYMENT", "REPLICAS", "AVAILABLE", "UP-TO-DATE")

	resourcesNames := []string{}
	for name := range mt.DeploymentsSpecs {
		resourcesNames = append(resourcesNames, name)
	}
	sort.Strings(resourcesNames)

	var tableChangesCount int
	for _, name := range resourcesNames {
		prevStatus := mt.PrevDeploymentsStatuses[name]
		status := mt.DeploymentsStatuses[name]
		spec := mt.DeploymentsSpecs[name]

		if stillDeployed := prevStatus.IsReady && status.IsReady; stillDeployed {
			continue
		}

		showProgress := status.StatusGeneration > prevStatus.StatusGeneration
		disableWarningColors := spec.FailMode == IgnoreAndContinueDeployProcess

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
			tableChangesCount++
			t.Row(resource, replicas, available, uptodate, formatResourceError(disableWarningColors, status.FailedReason))
		} else {
			tableChangesCount++
			t.Row(resource, replicas, available, uptodate)
		}

		if len(status.Pods) > 0 {
			st, podTableChangesCount := mt.displayChildPodsStatusProgress(&t, prevStatus.Pods, status.Pods, status.NewPodsNames, spec.FailMode, showProgress, disableWarningColors)
			tableChangesCount = tableChangesCount + podTableChangesCount
			extraMsg := ""
			if len(status.WaitingForMessages) > 0 {
				tableChangesCount++
				extraMsg += "---\n"
				extraMsg += utils.BlueF("Waiting for: %s", strings.Join(status.WaitingForMessages, ", "))
			}
			st.Commit(extraMsg)
		}

		mt.PrevDeploymentsStatuses[name] = status
	}

	if tableChangesCount > 0 {
		logboek.Context(context.Background()).Log(t.Render())
	}
}

func (mt *multitracker) displayGenericsStatusProgress() {
	t := utils.NewTable([]float64{.43, .14, .43}...)
	t.SetWidth(logboek.Context(context.Background()).Streams().ContentWidth() - 1)
	t.Header("RESOURCE", "NAMESPACE", "CONDITION: CURRENT (DESIRED)")

	var tableChangesCount int
	for _, resource := range mt.GenericResources {
		var namespace string
		if resource.Spec.Namespace != "" {
			namespace = resource.Spec.Namespace
		} else {
			namespace = "-"
		}

		lastStatus := resource.State.LastStatus()
		if lastStatus == nil {
			resourceCaption := formatGenericResourceCaption(resource.Spec.ResourceID.KindNameString(), resource.Spec.FailMode, false, false, true)
			tableChangesCount++
			t.Row(resourceCaption, namespace, "-")
			continue
		}

		lastPrintedStatus := resource.State.LastPrintedStatus()

		if stillReady := lastPrintedStatus != nil && lastPrintedStatus.IsReady() && lastStatus.IsReady(); stillReady {
			continue
		}

		var showProgress bool
		if lastPrintedStatus != nil {
			showProgress = lastPrintedStatus.DiffersFrom(lastStatus)
		} else {
			showProgress = true
		}

		resourceCaption := formatGenericResourceCaption(resource.Spec.ResourceID.KindNameString(), resource.Spec.FailMode, lastStatus.IsReady(), lastStatus.IsFailed(), true)

		var lastPrintedStatusIndicator *indicators.StringEqualConditionIndicator
		if lastPrintedStatus != nil {
			lastPrintedStatusIndicator = lastPrintedStatus.Indicator
		}

		disableWarningColors := resource.Spec.FailMode == generic.IgnoreAndContinueDeployProcess

		var currentAndDesiredState string
		if lastStatus.Indicator != nil {
			if lastStatus.IsFailed() && lastStatus.Indicator.FailedValue == "" {
				currentAndDesiredState = "-"
			} else {
				currentAndDesiredState = lastStatus.Indicator.FormatTableElem(lastPrintedStatusIndicator, indicators.FormatTableElemOptions{
					ShowProgress:         showProgress,
					DisableWarningColors: disableWarningColors,
					WithTargetValue:      true,
				})
			}
		} else {
			currentAndDesiredState = "-"
		}

		var condition string
		if lastStatus.HumanConditionPath() != "" {
			condition = fmt.Sprintf("%s: %s", lastStatus.HumanConditionPath(), currentAndDesiredState)
		} else {
			condition = "-"
		}

		tableChangesCount++
		if lastStatus.IsFailed() && lastStatus.FailureReason() != "" {
			t.Row(resourceCaption, namespace, condition, formatResourceError(disableWarningColors, lastStatus.FailureReason()))
		} else {
			t.Row(resourceCaption, namespace, condition)
		}

		resource.State.SetLastPrintedStatus(lastStatus)
	}

	if tableChangesCount > 0 {
		logboek.Context(context.Background()).Log(t.Render())
	}
}

func (mt *multitracker) displayChildPodsStatusProgress(t *utils.Table, prevPods, pods map[string]pod.PodStatus, newPodsNames []string, failMode FailMode, showProgress, disableWarningColors bool) (st *utils.Table, tableChangesCount int) {
	{
		subT := t.SubTable(statusProgressSubTableRatio...)
		st = &subT
	}

	st.Header("POD", "READY", "RESTARTS", "STATUS")

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

		podRow = append(podRow, resource, ready, podStatus.Restarts, status)
		if podStatus.IsFailed {
			podRow = append(podRow, formatResourceError(disableWarningColors, podStatus.FailedReason))
		}

		podRows = append(podRows, podRow)
	}

	st.Rows(podRows...)

	return st, len(podRows)
}

func formatResourceWarning(disableWarningColors bool, reason string) string {
	msg := fmt.Sprintf("warning: %s", reason)
	if disableWarningColors {
		return msg
	}
	return utils.YellowF("%s", msg)
}

func formatResourceError(disableWarningColors bool, reason string) string {
	msg := fmt.Sprintf("error: %s", reason)
	if disableWarningColors {
		return msg
	}
	return utils.RedF("%s", msg)
}

func formatResourceCaption(resourceCaption string, resourceFailMode FailMode, isReady, isFailed, isNew bool) string {
	if !isNew {
		return resourceCaption
	}

	switch resourceFailMode {
	case FailWholeDeployProcessImmediately:
		switch {
		case isReady:
			return utils.GreenF("%s", resourceCaption)
		case isFailed:
			return utils.RedF("%s", resourceCaption)
		default:
			return utils.YellowF("%s", resourceCaption)
		}

	case IgnoreAndContinueDeployProcess:
		if isReady {
			return utils.GreenF("%s", resourceCaption)
		} else {
			return resourceCaption
		}

	case HopeUntilEndOfDeployProcess:
		if isReady {
			return utils.GreenF("%s", resourceCaption)
		} else {
			return utils.YellowF("%s", resourceCaption)
		}

	default:
		panic(fmt.Sprintf("unsupported resource fail mode '%s'", resourceFailMode))
	}
}

func formatGenericResourceCaption(resourceCaption string, resourceFailMode generic.FailMode, isReady, isFailed, isNew bool) string {
	if !isNew {
		return resourceCaption
	}

	switch resourceFailMode {
	case generic.FailWholeDeployProcessImmediately:
		switch {
		case isReady:
			return utils.GreenF("%s", resourceCaption)
		case isFailed:
			return utils.RedF("%s", resourceCaption)
		default:
			return utils.YellowF("%s", resourceCaption)
		}

	case generic.IgnoreAndContinueDeployProcess:
		if isReady {
			return utils.GreenF("%s", resourceCaption)
		} else {
			return resourceCaption
		}

	case generic.HopeUntilEndOfDeployProcess:
		if isReady {
			return utils.GreenF("%s", resourceCaption)
		} else {
			return utils.YellowF("%s", resourceCaption)
		}

	default:
		panic(fmt.Sprintf("unsupported resource fail mode '%s'", resourceFailMode))
	}
}

func podContainerLogChunkHeader(podName string, chunk *pod.ContainerLogChunk) string {
	return fmt.Sprintf("po/%s container/%s", podName, chunk.ContainerName)
}
