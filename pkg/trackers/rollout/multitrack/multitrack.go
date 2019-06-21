package multitrack

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"

	"k8s.io/client-go/kubernetes"

	"github.com/flant/logboek"

	"github.com/flant/kubedog/pkg/display"
	"github.com/flant/kubedog/pkg/tracker"
	"github.com/flant/kubedog/pkg/tracker/daemonset"
	"github.com/flant/kubedog/pkg/tracker/deployment"
	"github.com/flant/kubedog/pkg/tracker/indicators"
	"github.com/flant/kubedog/pkg/tracker/job"
	"github.com/flant/kubedog/pkg/tracker/pod"
	"github.com/flant/kubedog/pkg/tracker/statefulset"
	"github.com/flant/kubedog/pkg/utils"
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
		DeploymentsStatuses:     make(map[string]deployment.DeploymentStatus),
		PrevDeploymentsStatuses: make(map[string]deployment.DeploymentStatus),

		StatefulSetsSpecs:        make(map[string]MultitrackSpec),
		TrackingStatefulSets:     make(map[string]*multitrackerResourceState),
		StatefulSetsStatuses:     make(map[string]statefulset.StatefulSetStatus),
		PrevStatefulSetsStatuses: make(map[string]statefulset.StatefulSetStatus),

		DaemonSetsSpecs:        make(map[string]MultitrackSpec),
		TrackingDaemonSets:     make(map[string]*multitrackerResourceState),
		DaemonSetsStatuses:     make(map[string]daemonset.DaemonSetStatus),
		PrevDaemonSetsStatuses: make(map[string]daemonset.DaemonSetStatus),

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
		mt.StatefulSetsSpecs[spec.ResourceName] = spec
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
		mt.DaemonSetsSpecs[spec.ResourceName] = spec
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
	DeploymentsStatuses     map[string]deployment.DeploymentStatus
	PrevDeploymentsStatuses map[string]deployment.DeploymentStatus

	StatefulSetsSpecs        map[string]MultitrackSpec
	TrackingStatefulSets     map[string]*multitrackerResourceState
	StatefulSetsStatuses     map[string]statefulset.StatefulSetStatus
	PrevStatefulSetsStatuses map[string]statefulset.StatefulSetStatus

	DaemonSetsSpecs        map[string]MultitrackSpec
	TrackingDaemonSets     map[string]*multitrackerResourceState
	DaemonSetsStatuses     map[string]daemonset.DaemonSetStatus
	PrevDaemonSetsStatuses map[string]daemonset.DaemonSetStatus

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

func (mt *multitracker) printChildPodsStatusProgress(t *utils.Table, prevPods map[string]pod.PodStatus, pods map[string]pod.PodStatus, newPodsNames []string, failMode FailMode, showProgress, disableWarningColors bool) *utils.Table {
	st := t.SubTable(.3, .16, .2, .16, .16)
	st.Header("NAME", "RDY", "STATUS", "RESTARTS", "AGE")

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

		resource := formatResourceCaption(strings.Join(strings.Split(podName, "-")[1:], "-"), failMode, podStatus.IsReady, podStatus.IsFailed, isPodNew)
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
			podRow = append(podRow, color.New(color.FgRed).Sprintf("Error: %s", podStatus.FailedReason))
		}

		podRows = append(podRows, podRow)
	}

	st.Rows(podRows...)

	return &st
}

func (mt *multitracker) printDeploymentsStatusProgress() {
	t := utils.NewTable(.7, .1, .1, .1)
	t.SetWidth(logboek.ContentWidth() - 1)
	t.Header("DEPLOYMENT", "REPLICAS", "AVAILABLE", "UP-TO-DATE")

	resourcesNames := []string{}
	for name := range mt.DeploymentsStatuses {
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
			t.Row(resource, replicas, available, uptodate, color.New(color.FgRed).Sprintf("Error: %s", status.FailedReason))
		} else {
			t.Row(resource, replicas, available, uptodate)
		}

		if len(status.Pods) > 0 {
			st := mt.printChildPodsStatusProgress(&t, prevStatus.Pods, status.Pods, status.NewPodsNames, spec.FailMode, showProgress, disableWarningColors)
			extraMsg := ""
			if len(status.WaitingForMessages) > 0 {
				extraMsg += "---\n"
				extraMsg += color.New(color.FgBlue).Sprintf("Waiting for: %s", strings.Join(status.WaitingForMessages, ", "))
			}
			st.Commit(extraMsg)
		}

		mt.PrevDeploymentsStatuses[name] = status
	}

	_, _ = logboek.OutF(t.Render())
}

func (mt *multitracker) printDaemonSetsStatusProgress() {
	t := utils.NewTable(.7, .1, .1, .1)
	t.SetWidth(logboek.ContentWidth() - 1)
	t.Header("DAEMONSET", "REPLICAS", "AVAILABLE", "UP-TO-DATE")

	resourcesNames := []string{}
	for name := range mt.DaemonSetsStatuses {
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
			t.Row(resource, replicas, available, uptodate, color.New(color.FgRed).Sprintf("Error: %s", status.FailedReason))
		} else {
			t.Row(resource, replicas, available, uptodate)
		}

		if len(status.Pods) > 0 {
			st := mt.printChildPodsStatusProgress(&t, prevStatus.Pods, status.Pods, status.NewPodsNames, spec.FailMode, showProgress, disableWarningColors)
			extraMsg := ""
			if len(status.WaitingForMessages) > 0 {
				extraMsg += "---\n"
				extraMsg += color.New(color.FgBlue).Sprintf("Waiting for: %s", strings.Join(status.WaitingForMessages, ", "))
			}
			st.Commit(extraMsg)
		}

		mt.PrevDaemonSetsStatuses[name] = status
	}

	_, _ = logboek.OutF(t.Render())
}

func (mt *multitracker) printStatefulSetsStatusProgress() {
	t := utils.NewTable(.7, .1, .1, .1)
	t.SetWidth(logboek.ContentWidth() - 1)
	t.Header("STATEFULSET", "REPLICAS", "READY", "UP-TO-DATE")

	resourcesNames := []string{}
	for name := range mt.StatefulSetsStatuses {
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
			t.Row(resource, replicas, ready, uptodate, color.New(color.FgRed).Sprintf("Error: %s", status.FailedReason))
		} else {
			t.Row(resource, replicas, ready, uptodate)
		}

		if len(status.Pods) > 0 {
			st := mt.printChildPodsStatusProgress(&t, prevStatus.Pods, status.Pods, status.NewPodsNames, spec.FailMode, showProgress, disableWarningColors)
			extraMsg := ""
			if len(status.WaitingForMessages) > 0 {
				extraMsg += "---\n"
				extraMsg += color.New(color.FgBlue).Sprintf("Waiting for: %s", strings.Join(status.WaitingForMessages, ", "))
			}
			st.Commit(extraMsg)
		}

		mt.PrevStatefulSetsStatuses[name] = status
	}

	_, _ = logboek.OutF(t.Render())
}

func (mt *multitracker) PrintStatusProgress() error {
	caption := color.New(color.Bold).Sprint("Status progress")

	_ = logboek.LogProcess(caption, logboek.LogProcessOptions{}, func() error {
		mt.printDeploymentsStatusProgress()

		logboek.OutF("\n")

		mt.printDaemonSetsStatusProgress()

		logboek.OutF("\n")

		mt.printStatefulSetsStatusProgress()

		return nil
	})

	// mt.printStatefulSetsStatusProgress()

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
	// 			display.OutF("│   %s\n", color.New(color.FgGreen).Sprintf("✅ %s", cond.Message))// }

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
