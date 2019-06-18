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

func (mt *multitracker) printDeploymentsStatusProgress() {
	t := utils.NewTable(.55, .15, .15, .15)
	t.Header("NAME", "REPLICAS", "UP-TO-DATE", "AVAILABLE")

	// t.Raw("deploy/extended-monitoring", "1/1", 1, 1)
	//t.Raw("deploy/extended-monitoring", "1/1", 1, 1, color.RedString("Error: See the server log for details. BUILD FAILED (total time: 1 second)"), color.RedString("Error: An individual language user's deviations from standard language norms in grammar, pronunciation and orthography are sometimes referred to as errors"))
	// st := t.SubTable(.4, .1, .3, .1, .1)
	// st.Header("NAME", "READY", "STATUS", "RESTARTS", "AGE")
	// st.Raws([][]interface{}{
	// 	{"654fc55df-5zs4m", "3/3", "Pulling", "0", "49m", color.RedString("pod/myapp-backend-cbdb856d7-bvplx Failed: Error: ImagePullBackOff"), color.RedString("pod/myapp-backend-cbdb856d7-b6ms8 Failed: Failed to pull image \"ubuntu:kaka\": rpc error: code Unknown desc = Error response from daemon: manifest for ubuntu:kaka not found")},
	// 	{"654fc55df-hsm67", "3/3", color.GreenString("Running") + " -> " + color.RedString("Terminating"), "0", "49m"},
	// 	{"654fc55df-fffff", "3/3", "Ready", "0", "49m"},
	// }...)
	// t.Raw("deploy/grafana", "1/1", 1, 1)
	// t.Raw("deploy/kube-state-metrics", "1/1", 1, 1)
	// t.Raw("deploy/madison-proxy-0450d21f50d1e3f3b3131a07bcbcfe85ec02dd9758b7ee12968ee6eaee7057fc", "1/1", 1, 1)
	// t.Raw("deploy/madison-proxy-2c5bdd9ba9f80394e478714dc299d007182bc49fed6c319d67b6645e4812b198", "1/1", 1, 1)
	// t.Raw("deploy/madison-proxy-9c6b5f859895442cb645c7f3d1ef647e1ed5388c159a9e5f7e1cf50163a878c1", "1/1", 1, "1 (-1)")
	// t.Raw("deploy/prometheus-metrics-adapter", "1/1", 1, "1 (-1)")
	// t.Raw("sts/mysql", "1/1", 1, "1 (-1)")
	// t.Raw("ds/node-exporter", "1/1", 1, "1 (-1)")
	// t.Raw("deploy/trickster", "1/1", 1, "1 (-1)")
	// t.Render()

	resourcesNames := []string{}
	for name := range mt.DeploymentsStatuses {
		resourcesNames = append(resourcesNames, name)
	}
	sort.Strings(resourcesNames)

	for _, name := range resourcesNames {
		prevStatus := mt.PrevDeploymentsStatuses[name]
		status := mt.DeploymentsStatuses[name]

		spec := mt.DeploymentsSpecs[name]

		formatOpts := indicators.FormatTableElemOptions{
			ShowProgress:         (status.StatusGeneration > prevStatus.StatusGeneration),
			DisableWarningColors: spec.FailMode == IgnoreAndContinueDeployProcess,
		}

		resource := formatResourceCaption(fmt.Sprintf("deploy/%s", name), spec.FailMode, status.IsReady, status.IsFailed)
		overall := status.OverallReplicasIndicator.FormatTableElem(prevStatus.OverallReplicasIndicator, formatOpts)
		uptodate := status.UpdatedReplicasIndicator.FormatTableElem(prevStatus.UpdatedReplicasIndicator, formatOpts)
		available := status.AvailableReplicasIndicator.FormatTableElem(prevStatus.AvailableReplicasIndicator, formatOpts)

		if status.IsFailed {
			t.Raw(resource, overall, uptodate, available, color.New(color.FgRed).Sprintf("Error: %s", status.FailedReason))
		} else {
			t.Raw(resource, overall, uptodate, available)
		}

		if len(status.Pods) > 0 {
			st := t.SubTable(.3, .15, .25, .15, .15)
			st.Header("NAME", "READY", "STATUS", "RESTARTS", "AGE")

			podsNames := []string{}
			for podName := range status.Pods {
				podsNames = append(podsNames, podName)
			}
			sort.Strings(podsNames)

			for _, podName := range podsNames {
				prevPodStatus := prevStatus.Pods[podName]
				podStatus := status.Pods[podName]

				resource := formatResourceCaption(fmt.Sprintf("po/%s", podName), spec.FailMode, podStatus.IsReady, podStatus.IsFailed)
				ready := fmt.Sprintf("%d/%d", podStatus.ReadyContainers, podStatus.TotalContainers)
				status := podStatus.StatusIndicator.FormatTableElem(prevPodStatus.StatusIndicator, formatOpts)

				if podStatus.IsFailed {
					st.Raw(resource, ready, status, podStatus.Restarts, podStatus.Age, color.New(color.FgRed).Sprintf("Error: %s", podStatus.FailedReason))
				} else {
					st.Raw(resource, ready, status, podStatus.Restarts, podStatus.Age)
				}
			}
		}

		mt.PrevDeploymentsStatuses[name] = status
	}

	t.Render()
}

func (mt *multitracker) printStatefulSetsStatusProgress() {
	newlineNeeded := false
	resourcesNames := []string{}
	for name := range mt.StatefulSetsStatuses {
		resourcesNames = append(resourcesNames, name)
	}
	sort.Strings(resourcesNames)

	for _, name := range resourcesNames {
		prevStatus := mt.PrevStatefulSetsStatuses[name]
		status := mt.StatefulSetsStatuses[name]

		spec := mt.StatefulSetsSpecs[name]

		formatOpts := indicators.FormatTableElemOptions{
			ShowProgress:         (status.StatusGeneration > prevStatus.StatusGeneration),
			DisableWarningColors: spec.FailMode == IgnoreAndContinueDeployProcess,
		}

		resource := formatResourceCaption(fmt.Sprintf("sts/%s", name), spec.FailMode, status.IsReady, status.IsFailed)

		current := "-"
		if status.OverallReplicasIndicator != nil {
			current = status.OverallReplicasIndicator.FormatTableElem(prevStatus.OverallReplicasIndicator, formatOpts)
		}

		if newlineNeeded {
			display.OutF("│\n")
		}

		display.OutF("│ %s %s %s %s %s\n", formatColorItemByWidth(resource, 30), formatColorItemByWidth(current, 15), formatColorItemByWidth("", 15), formatColorItemByWidth("", 15), formatColorItemByWidth("", 15))

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

			resource := formatResourceCaption(fmt.Sprintf("po/%s", podName), spec.FailMode, podStatus.IsReady, podStatus.IsFailed)
			restartsStr := fmt.Sprintf("%d", podStatus.Restarts)
			ready := fmt.Sprintf("%d/%d", podStatus.ReadyContainers, podStatus.TotalContainers)
			status := podStatus.StatusIndicator.FormatTableElem(prevPodStatus.StatusIndicator, formatOpts)

			display.OutF("│           %s %s %s %s %s\n", formatColorItemByWidth(resource, 30), formatColorItemByWidth(ready, 15), formatColorItemByWidth(status, 25), formatColorItemByWidth(restartsStr, 15), formatColorItemByWidth(podStatus.Age, 15))

			if podStatus.IsFailed {
				display.OutF("│           %s %s\n", formatColorItemByWidth(resource, 30), color.New(color.FgRed).Sprintf("%s", podStatus.FailedReason))
			}
		}

		mt.PrevStatefulSetsStatuses[name] = status
	}
}

func (mt *multitracker) PrintStatusProgress() error {
	caption := color.New(color.Bold).Sprint("Status progress")

	display.OutF("\n┌ %s\n", caption)

	mt.printDeploymentsStatusProgress()
	// mt.printStatefulSetsStatusProgress()

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
