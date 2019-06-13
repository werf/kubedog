package multitrack

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/acarl005/stripansi"
	"github.com/fatih/color"

	"k8s.io/client-go/kubernetes"

	"github.com/flant/kubedog/pkg/display"
	"github.com/flant/kubedog/pkg/tracker"
	"github.com/flant/kubedog/pkg/tracker/daemonset"
	"github.com/flant/kubedog/pkg/tracker/deployment"
	"github.com/flant/kubedog/pkg/tracker/indicators"
	"github.com/flant/kubedog/pkg/tracker/job"
	"github.com/flant/kubedog/pkg/tracker/pod"
	"github.com/flant/kubedog/pkg/tracker/statefulset"
)

type FailMode string

const (
	IgnoreAndContinueDeployProcess    FailMode = "IgnoreAndContinueDeployProcess"
	FailWholeDeployProcessImmediately FailMode = "FailWholeDeployProcessImmediately"
	HopeUntilEndOfDeployProcess       FailMode = "HopeUntilEndOfDeployProcess"
)

type DeployCondition string

const (
	ControllerIsReady DeployCondition = "ControllerIsReady"
	PodIsReady        DeployCondition = "PodIsReady"
	EndOfDeploy       DeployCondition = "EndOfDeploy"
)

type MultitrackSpecs struct {
	Pods         []MultitrackSpec
	Deployments  []MultitrackSpec
	StatefulSets []MultitrackSpec
	DaemonSets   []MultitrackSpec
	Jobs         []MultitrackSpec
}

type MultitrackSpec struct {
	ResourceName string
	Namespace    string

	FailMode                FailMode
	AllowFailuresCount      *int
	FailureThresholdSeconds *int

	LogRegex                *regexp.Regexp
	LogRegexByContainerName map[string]*regexp.Regexp

	SkipLogs                  bool
	SkipLogsForContainers     []string
	ShowLogsOnlyForContainers []string
	ShowLogsUntil             DeployCondition

	SkipEvents bool
}

type MultitrackOptions struct {
	tracker.Options
}

func setDefaultSpecValues(spec *MultitrackSpec) {
	if spec.FailMode == "" {
		spec.FailMode = FailWholeDeployProcessImmediately
	}

	if spec.AllowFailuresCount == nil {
		spec.AllowFailuresCount = new(int)
		*spec.AllowFailuresCount = 1
	}

	if spec.FailureThresholdSeconds == nil {
		spec.FailureThresholdSeconds = new(int)
		*spec.FailureThresholdSeconds = 0
	}

	if spec.ShowLogsUntil == "" {
		spec.ShowLogsUntil = PodIsReady
	}
}

func Multitrack(kube kubernetes.Interface, specs MultitrackSpecs, opts MultitrackOptions) error {
	if len(specs.Pods)+len(specs.Deployments)+len(specs.StatefulSets)+len(specs.DaemonSets)+len(specs.Jobs) == 0 {
		return nil
	}

	for i := range specs.Pods {
		setDefaultSpecValues(&specs.Pods[i])
	}
	for i := range specs.Deployments {
		setDefaultSpecValues(&specs.Deployments[i])
	}
	for i := range specs.StatefulSets {
		setDefaultSpecValues(&specs.StatefulSets[i])
	}
	for i := range specs.DaemonSets {
		setDefaultSpecValues(&specs.DaemonSets[i])
	}
	for i := range specs.Jobs {
		setDefaultSpecValues(&specs.Jobs[i])
	}

	errorChan := make(chan error, 0)
	doneChan := make(chan struct{}, 0)

	mt := multitracker{
		TrackingPods: make(map[string]*multitrackerResourceState),
		PodsStatuses: make(map[string]pod.PodStatus),

		DeploymentsSpecs:        make(map[string]MultitrackSpec),
		TrackingDeployments:     make(map[string]*multitrackerResourceState),
		PrevDeploymentsStatuses: make(map[string]deployment.DeploymentStatus),
		DeploymentsStatuses:     make(map[string]deployment.DeploymentStatus),

		TrackingStatefulSets: make(map[string]*multitrackerResourceState),
		StatefulSetsStatuses: make(map[string]statefulset.StatefulSetStatus),

		TrackingDaemonSets: make(map[string]*multitrackerResourceState),
		DaemonSetsStatuses: make(map[string]daemonset.DaemonSetStatus),

		TrackingJobs: make(map[string]*multitrackerResourceState),
		JobsStatuses: make(map[string]job.JobStatus),
	}

	statusReportTicker := time.NewTicker(5 * time.Second)
	defer statusReportTicker.Stop()

	var wg sync.WaitGroup

	for _, spec := range specs.Pods {
		mt.TrackingPods[spec.ResourceName] = &multitrackerResourceState{}

		wg.Add(1)
		go func(spec MultitrackSpec) {
			if err := mt.TrackPod(kube, spec, opts); err != nil {
				errorChan <- fmt.Errorf("po/%s track failed: %s", spec.ResourceName, err)
			}
			wg.Done()
		}(spec)
	}
	for _, spec := range specs.Deployments {
		mt.DeploymentsSpecs[spec.ResourceName] = spec
		mt.TrackingDeployments[spec.ResourceName] = &multitrackerResourceState{}

		wg.Add(1)
		go func(spec MultitrackSpec) {
			if err := mt.TrackDeployment(kube, spec, opts); err != nil {
				errorChan <- fmt.Errorf("deploy/%s track failed: %s", spec.ResourceName, err)
			}
			wg.Done()
		}(spec)
	}
	for _, spec := range specs.StatefulSets {
		mt.TrackingStatefulSets[spec.ResourceName] = &multitrackerResourceState{}

		wg.Add(1)
		go func(spec MultitrackSpec) {
			if err := mt.TrackStatefulSet(kube, spec, opts); err != nil {
				errorChan <- fmt.Errorf("sts/%s track failed: %s", spec.ResourceName, err)
			}
			wg.Done()
		}(spec)
	}
	for _, spec := range specs.DaemonSets {
		mt.TrackingDaemonSets[spec.ResourceName] = &multitrackerResourceState{}

		wg.Add(1)
		go func(spec MultitrackSpec) {
			if err := mt.TrackDaemonSet(kube, spec, opts); err != nil {
				errorChan <- fmt.Errorf("ds/%s track failed: %s", spec.ResourceName, err)
			}
			wg.Done()
		}(spec)
	}
	for _, spec := range specs.Jobs {
		mt.TrackingJobs[spec.ResourceName] = &multitrackerResourceState{}

		wg.Add(1)
		go func(spec MultitrackSpec) {
			if err := mt.TrackJob(kube, spec, opts); err != nil {
				errorChan <- fmt.Errorf("job/%s track failed: %s", spec.ResourceName, err)
			}
			wg.Done()
		}(spec)
	}

	go func() {
		wg.Wait()

		err := func() error {
			mt.handlerMux.Lock()
			defer mt.handlerMux.Unlock()
			return mt.PrintStatusProgress()
		}()

		if err != nil {
			errorChan <- err
			return
		}

		if mt.hasFailedTrackingResources() {
			errorChan <- mt.formatFailedTrackingResourcesError()
		} else {
			doneChan <- struct{}{}
		}
	}()

	for {
		select {
		case <-statusReportTicker.C:
			err := func() error {
				mt.handlerMux.Lock()
				defer mt.handlerMux.Unlock()

				if err := mt.PrintStatusProgress(); err != nil {
					return err
				}

				return nil
			}()

			if err != nil {
				return err
			}

		case <-doneChan:
			return nil

		case err := <-errorChan:
			return err
		}
	}
}

type multitracker struct {
	TrackingPods map[string]*multitrackerResourceState
	PodsStatuses map[string]pod.PodStatus

	DeploymentsSpecs        map[string]MultitrackSpec
	TrackingDeployments     map[string]*multitrackerResourceState
	PrevDeploymentsStatuses map[string]deployment.DeploymentStatus
	DeploymentsStatuses     map[string]deployment.DeploymentStatus

	TrackingStatefulSets map[string]*multitrackerResourceState
	StatefulSetsStatuses map[string]statefulset.StatefulSetStatus

	TrackingDaemonSets map[string]*multitrackerResourceState
	DaemonSetsStatuses map[string]daemonset.DaemonSetStatus

	TrackingJobs map[string]*multitrackerResourceState
	JobsStatuses map[string]job.JobStatus

	handlerMux sync.Mutex
}

type multitrackerResourceState struct {
	IsFailed          bool
	LastFailureReason string
	FailuresCount     int
}

func (mt *multitracker) isTrackingAnyNonFailedResource() bool {
	for _, states := range []map[string]*multitrackerResourceState{
		mt.TrackingPods,
		mt.TrackingDeployments,
		mt.TrackingStatefulSets,
		mt.TrackingDaemonSets,
		mt.TrackingJobs,
	} {
		for _, state := range states {
			if !state.IsFailed {
				return true
			}
		}
	}

	return false
}

func (mt *multitracker) hasFailedTrackingResources() bool {
	for _, states := range []map[string]*multitrackerResourceState{
		mt.TrackingPods,
		mt.TrackingDeployments,
		mt.TrackingStatefulSets,
		mt.TrackingDaemonSets,
		mt.TrackingJobs,
	} {
		for _, state := range states {
			if state.IsFailed {
				return true
			}
		}
	}
	return false
}

func (mt *multitracker) formatFailedTrackingResourcesError() error {
	msgParts := []string{}

	for name, state := range mt.TrackingPods {
		if !state.IsFailed {
			continue
		}
		msgParts = append(msgParts, fmt.Sprintf("po/%s failed: %s", name, state.LastFailureReason))
	}
	for name, state := range mt.TrackingDeployments {
		if !state.IsFailed {
			continue
		}
		msgParts = append(msgParts, fmt.Sprintf("deploy/%s failed: %s", name, state.LastFailureReason))
	}
	for name, state := range mt.TrackingStatefulSets {
		if !state.IsFailed {
			continue
		}
		msgParts = append(msgParts, fmt.Sprintf("sts/%s failed: %s", name, state.LastFailureReason))
	}
	for name, state := range mt.TrackingDaemonSets {
		if !state.IsFailed {
			continue
		}
		msgParts = append(msgParts, fmt.Sprintf("ds/%s failed: %s", name, state.LastFailureReason))
	}
	for name, state := range mt.TrackingJobs {
		if !state.IsFailed {
			continue
		}
		msgParts = append(msgParts, fmt.Sprintf("job/%s failed: %s", name, state.LastFailureReason))
	}

	return fmt.Errorf("%s", strings.Join(msgParts, "\n"))
}

func (mt *multitracker) handleResourceReadyCondition(resourcesStates map[string]*multitrackerResourceState, spec MultitrackSpec) error {
	delete(resourcesStates, spec.ResourceName)
	return tracker.StopTrack
}

func formatColorItemByWidth(itemData string, width int) string {
	strippedItemData := stripansi.Strip(itemData)
	excessSymbols := len(itemData) - len(strippedItemData)
	return fmt.Sprintf(fmt.Sprintf("%%%ds", width+excessSymbols), itemData)
}

func formatResourceCaption(resourceCaption string, resourceFailMode FailMode, isReady bool, isFailed bool) string {
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

func (mt *multitracker) PrintStatusProgress() error {
	caption := color.New(color.Bold).Sprint("Status progress")

	display.OutF("\n┌ %s\n", caption)

	controllersCaption := fmt.Sprintf("%30s %15s %15s %15s %15s", "NAME", "CURRENT", "UP-TO-DATE", "AVAILABLE", "OLD")
	display.OutF("│ %s\n", controllersCaption)

	podsCaption := fmt.Sprintf("          %30s %15s %25s %15s %15s", "POD_NAME", "POD_READY", "POD_STATUS", "POD_RESTARTS", "POD_AGE")
	display.OutF("│ %s\n", podsCaption)
	display.OutF("│\n")

	newlineNeeded := false

	deploymentsNames := []string{}
	for name := range mt.DeploymentsStatuses {
		deploymentsNames = append(deploymentsNames, name)
	}
	sort.Strings(deploymentsNames)

	for _, name := range deploymentsNames {
		prevStatus := mt.PrevDeploymentsStatuses[name]
		status := mt.DeploymentsStatuses[name]

		spec := mt.DeploymentsSpecs[name]

		formatOpts := indicators.FormatTableElemOptions{
			ShowProgress:         (status.StatusGeneration > prevStatus.StatusGeneration),
			DisableWarningColors: spec.FailMode == IgnoreAndContinueDeployProcess,
		}

		resource := formatResourceCaption(fmt.Sprintf("deploy/%s", name), spec.FailMode, status.ReadyIndicator.IsReady, status.IsFailed)
		current := status.ReadyIndicator.OverallReplicasIndicator.FormatTableElem(prevStatus.ReadyIndicator.OverallReplicasIndicator, formatOpts)
		uptodate := status.ReadyIndicator.UpdatedReplicasIndicator.FormatTableElem(prevStatus.ReadyIndicator.UpdatedReplicasIndicator, formatOpts)
		available := status.ReadyIndicator.AvailableReplicasIndicator.FormatTableElem(prevStatus.ReadyIndicator.AvailableReplicasIndicator, formatOpts)
		old := status.ReadyIndicator.OldReplicasIndicator.FormatTableElem(prevStatus.ReadyIndicator.OldReplicasIndicator, formatOpts)

		if newlineNeeded {
			display.OutF("│\n")
		}

		display.OutF("│ %s %s %s %s %s\n", formatColorItemByWidth(resource, 30), formatColorItemByWidth(current, 15), formatColorItemByWidth(uptodate, 15), formatColorItemByWidth(available, 15), formatColorItemByWidth(old, 15))

		if status.IsFailed {
			display.OutF("│ %s %s\n", formatColorItemByWidth(resource, 30), color.New(color.FgRed).Sprintf("%s", status.FailedReason))
		}

		if len(status.Pods) > 0 {
			display.OutF("│\n")
			newlineNeeded = true
		}

		podsNames := []string{}
		for podName := range status.Pods {
			podsNames = append(podsNames, podName)
		}
		sort.Strings(podsNames)

		for _, podName := range podsNames {
			prevPodStatus := prevStatus.Pods[podName]
			podStatus := status.Pods[podName]

			resource := formatResourceCaption(fmt.Sprintf("po/%s", podName), spec.FailMode, podStatus.ReadyIndicator.IsReady, podStatus.IsFailed)

			containersCount := 0
			readyContainersCount := 0
			restarts := int32(0)
			for _, cs := range podStatus.ContainerStatuses {
				containersCount++

				if cs.Ready {
					readyContainersCount++
				}

				restarts += cs.RestartCount
			}
			restartsStr := fmt.Sprintf("%d", restarts)

			ready := fmt.Sprintf("%d/%d", readyContainersCount, containersCount)

			phase := podStatus.ReadyIndicator.PhaseIndicator.FormatTableElem(prevPodStatus.ReadyIndicator.PhaseIndicator, formatOpts)

			display.OutF("│           %s %s %s %s %s\n", formatColorItemByWidth(resource, 30), formatColorItemByWidth(ready, 15), formatColorItemByWidth(phase, 25), formatColorItemByWidth(restartsStr, 15), formatColorItemByWidth("<unknown>", 15))

			if podStatus.IsFailed {
				display.OutF("│           %s %s\n", formatColorItemByWidth(resource, 30), color.New(color.FgRed).Sprintf("%s", podStatus.FailedReason))
			}
		}

		mt.PrevDeploymentsStatuses[name] = status
	}

	display.OutF("└ %s\n", caption)

	// for name, status := range mt.PodsStatuses {
	// 	display.OutF("├ po/%s\n", name)

	// 	if status.Phase != "" {
	// 		display.OutF("│   Phase:%s\n", status.Phase)
	// 	}

	// 	if len(status.Conditions) > 0 {
	// 		display.OutF("│   Conditions:\n")
	// 	}
	// 	for _, cond := range status.Conditions {
	// 		display.OutF("│   - %s %s:%s", cond.LastTransitionTime, cond.Type, cond.Status)
	// 		if cond.Reason != "" {
	// 			display.OutF(" %s", cond.Reason)
	// 		}
	// 		if cond.Message != "" {
	// 			display.OutF(" %s", cond.Message)
	// 		}
	// 		display.OutF("\n")
	// 	}
	// }

	// unreadyMsgs := []string{}
	// for _, cond := range status.ReadyStatus.ReadyConditions {
	// 	if !cond.IsSatisfied {
	// 		unreadyMsgs = append(unreadyMsgs, cond.Message)
	// 	}
	// }
	// if len(unreadyMsgs) > 0 {
	// 	display.OutF("│   %s\n", color.New(color.FgYellow).Sprintf("⌚ %s", strings.Join(unreadyMsgs, ", ")))
	// }

	// for _, cond := range status.ReadyStatus.ReadyConditions {
	// 	if cond.IsSatisfied {
	// 		if _, hasKey := mt.ShownDeploymentMessages[name][cond.Message]; !hasKey {
	// 			display.OutF("│   %s\n", color.New(color.FgGreen).Sprintf("✅ %s", cond.Message))
	// 			mt.ShownDeploymentMessages[name][cond.Message] = struct{}{}
	// 		}
	// 	}
	// }

	// 		for podName, podStatus := range status.Pods {
	// 			if podStatus.IsFailed {
	// 				display.OutF("│   %s\n", color.New(color.FgRed).Sprintf("❌ pod/%s %s", podName, podStatus.FailedReason))
	// 			}
	// 		}
	// 	}
	// }

	// for name, status := range mt.StatefulSetsStatuses {
	// 	display.OutF("├ sts/%s\n", name)
	// 	display.OutF("│   Replicas:%d ReadyReplicas:%d CurrentReplicas:%d UpdatedReplicas:%d\n", status.Replicas, status.ReadyReplicas, status.CurrentReplicas, status.UpdatedReplicas)
	// 	if len(status.Conditions) > 0 {
	// 		display.OutF("│   Conditions:\n")
	// 	}
	// 	for _, cond := range status.Conditions {
	// 		display.OutF("│   - %s %s:%s", cond.LastTransitionTime, cond.Type, cond.Status)
	// 		if cond.Reason != "" {
	// 			display.OutF(" %s", cond.Reason)
	// 		}
	// 		if cond.Message != "" {
	// 			display.OutF(" %s", cond.Message)
	// 		}
	// 		display.OutF("\n")
	// 	}
	// }

	// for name, status := range mt.DaemonSetsStatuses {
	// 	display.OutF("├ ds/%s\n", name)
	// 	display.OutF("│   CurrentNumberScheduled:%d NumberReady:%d NumberAvailable:%d NumberUnavailable:%d\n", status.CurrentNumberScheduled, status.NumberReady, status.NumberAvailable, status.NumberUnavailable)
	// 	if len(status.Conditions) > 0 {
	// 		display.OutF("│   Conditions:\n")
	// 	}
	// 	for _, cond := range status.Conditions {
	// 		display.OutF("│   - %s %s:%s", cond.LastTransitionTime, cond.Type, cond.Status)
	// 		if cond.Reason != "" {
	// 			display.OutF(" %s", cond.Reason)
	// 		}
	// 		if cond.Message != "" {
	// 			display.OutF(" %s", cond.Message)
	// 		}
	// 		display.OutF("\n")
	// 	}
	// }

	// for name, status := range mt.JobsStatuses {
	// 	display.OutF("├ job/%s\n", name)
	// 	display.OutF("│   Active:%d Succeeded:%d Failed:%d\n", status.Active, status.Succeeded, status.Failed)
	// 	display.OutF("│   StartTime:%s CompletionTime:%s\n", status.StartTime, status.CompletionTime)
	// 	if len(status.Conditions) > 0 {
	// 		display.OutF("│   Conditions:\n")
	// 	}
	// 	for _, cond := range status.Conditions {
	// 		display.OutF("│   - %s %s:%s", cond.LastTransitionTime, cond.Type, cond.Status)
	// 		if cond.Reason != "" {
	// 			display.OutF(" %s", cond.Reason)
	// 		}
	// 		if cond.Message != "" {
	// 			display.OutF(" %s", cond.Message)
	// 		}
	// 		display.OutF("\n")
	// 	}
	// }

	// for name := range mt.TrackingPods {
	// 	if _, hasKey := mt.PodsStatuses[name]; hasKey {
	// 		continue
	// 	}
	// 	display.OutF("├ po/%s status unavailable\n", name)
	// }
	// for name := range mt.TrackingDeployments {
	// 	if _, hasKey := mt.DeploymentsStatuses[name]; hasKey {
	// 		continue
	// 	}
	// 	display.OutF("├ deploy/%s status unavailable\n", name)
	// }
	// for name := range mt.TrackingStatefulSets {
	// 	if _, hasKey := mt.StatefulSetsStatuses[name]; hasKey {
	// 		continue
	// 	}
	// 	display.OutF("├ sts/%s status unavailable\n", name)
	// }
	// for name := range mt.TrackingDaemonSets {
	// 	if _, hasKey := mt.DaemonSetsStatuses[name]; hasKey {
	// 		continue
	// 	}
	// 	display.OutF("├ ds/%s status unavailable\n", name)
	// }
	// for name := range mt.TrackingJobs {
	// 	if _, hasKey := mt.JobsStatuses[name]; hasKey {
	// 		continue
	// 	}
	// 	display.OutF("├ job/%s status unavailable\n", name)
	// }

	return nil
}

func (mt *multitracker) handleResourceFailure(resourcesStates map[string]*multitrackerResourceState, spec MultitrackSpec, reason string) error {
	resourcesStates[spec.ResourceName].FailuresCount++
	if resourcesStates[spec.ResourceName].FailuresCount <= *spec.AllowFailuresCount {
		return nil
	}

	if spec.FailMode == FailWholeDeployProcessImmediately {
		resourcesStates[spec.ResourceName].IsFailed = true
		resourcesStates[spec.ResourceName].LastFailureReason = reason
		return tracker.StopTrack
	} else if spec.FailMode == HopeUntilEndOfDeployProcess {
		resourcesStates[spec.ResourceName].IsFailed = true
		resourcesStates[spec.ResourceName].LastFailureReason = reason
		// TODO: goroutine for this resource should be stopped somehow at the end of deploy process
		return nil
	} else if spec.FailMode == IgnoreAndContinueDeployProcess {
		delete(resourcesStates, spec.ResourceName)
		return tracker.StopTrack
	} else {
		panic(fmt.Sprintf("bad fail mode: %s", spec.FailMode))
	}
}

func displayContainerLogChunk(header string, spec MultitrackSpec, chunk *pod.ContainerLogChunk) {
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

	if logRegexp != nil {
		for _, logLine := range chunk.LogLines {
			message := logRegexp.FindString(logLine.Message)
			if message != "" {
				display.OutputLogLines(header, []display.LogLine{logLine})
			}
		}
	} else {
		display.OutputLogLines(header, chunk.LogLines)
	}
}
