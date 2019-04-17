package multitrack

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/flant/kubedog/pkg/display"
	"github.com/flant/kubedog/pkg/tracker"
	"github.com/flant/kubedog/pkg/tracker/daemonset"
	"github.com/flant/kubedog/pkg/tracker/deployment"
	"github.com/flant/kubedog/pkg/tracker/job"
	"github.com/flant/kubedog/pkg/tracker/pod"
	"github.com/flant/kubedog/pkg/tracker/statefulset"

	"k8s.io/client-go/kubernetes"
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

	LogWatchRegex                string
	LogWatchRegexByContainerName map[string]string
	ShowLogsUntil                DeployCondition
	SkipLogsForContainers        []string
	ShowLogsOnlyForContainers    []string
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

		TrackingDeployments: make(map[string]*multitrackerResourceState),
		DeploymentsStatuses: make(map[string]deployment.DeploymentStatus),

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
			return mt.PrintStatusReport()
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

				if err := mt.PrintStatusReport(); err != nil {
					return err
				}

				time.Sleep(time.Duration(5) * time.Second)

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

	TrackingDeployments map[string]*multitrackerResourceState
	DeploymentsStatuses map[string]deployment.DeploymentStatus

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

func (mt *multitracker) PrintStatusReport() error {
	display.OutF("\n┌ Status Report\n")

	for name, status := range mt.PodsStatuses {
		display.OutF("├ po/%s\n", name)

		if status.Phase != "" {
			display.OutF("│   Phase:%s\n", status.Phase)
		}

		if len(status.Conditions) > 0 {
			display.OutF("│   Conditions:\n")
		}
		for _, cond := range status.Conditions {
			display.OutF("│   - %s %s:%s", cond.LastTransitionTime, cond.Type, cond.Status)
			if cond.Reason != "" {
				display.OutF(" %s", cond.Reason)
			}
			if cond.Message != "" {
				display.OutF(" %s", cond.Message)
			}
			display.OutF("\n")
		}
	}

	for name, status := range mt.DeploymentsStatuses {
		display.OutF("├ deploy/%s\n", name)
		display.OutF("│   Replicas:%d UpdatedReplicas:%d ReadyReplicas:%d AvailableReplicas:%d UnavailableReplicas:%d\n", status.Replicas, status.UpdatedReplicas, status.ReadyReplicas, status.AvailableReplicas, status.UnavailableReplicas)
		if len(status.Conditions) > 0 {
			display.OutF("│   Conditions:\n")
		}
		for _, cond := range status.Conditions {
			display.OutF("│   - %s %s:%s", cond.LastTransitionTime, cond.Type, cond.Status)
			if cond.Reason != "" {
				display.OutF(" %s", cond.Reason)
			}
			if cond.Message != "" {
				display.OutF(" %s", cond.Message)
			}
			display.OutF("\n")
		}
	}

	for name, status := range mt.StatefulSetsStatuses {
		display.OutF("├ sts/%s\n", name)
		display.OutF("│   Replicas:%d ReadyReplicas:%d CurrentReplicas:%d UpdatedReplicas:%d\n", status.Replicas, status.ReadyReplicas, status.CurrentReplicas, status.UpdatedReplicas)
		if len(status.Conditions) > 0 {
			display.OutF("│   Conditions:\n")
		}
		for _, cond := range status.Conditions {
			display.OutF("│   - %s %s:%s", cond.LastTransitionTime, cond.Type, cond.Status)
			if cond.Reason != "" {
				display.OutF(" %s", cond.Reason)
			}
			if cond.Message != "" {
				display.OutF(" %s", cond.Message)
			}
			display.OutF("\n")
		}
	}

	for name, status := range mt.DaemonSetsStatuses {
		display.OutF("├ ds/%s\n", name)
		display.OutF("│   CurrentNumberScheduled:%d NumberReady:%d NumberAvailable:%d NumberUnavailable:%d\n", status.CurrentNumberScheduled, status.NumberReady, status.NumberAvailable, status.NumberUnavailable)
		if len(status.Conditions) > 0 {
			display.OutF("│   Conditions:\n")
		}
		for _, cond := range status.Conditions {
			display.OutF("│   - %s %s:%s", cond.LastTransitionTime, cond.Type, cond.Status)
			if cond.Reason != "" {
				display.OutF(" %s", cond.Reason)
			}
			if cond.Message != "" {
				display.OutF(" %s", cond.Message)
			}
			display.OutF("\n")
		}
	}

	for name, status := range mt.JobsStatuses {
		display.OutF("├ job/%s\n", name)
		display.OutF("│   Active:%d Succeeded:%d Failed:%d\n", status.Active, status.Succeeded, status.Failed)
		display.OutF("│   StartTime:%s CompletionTime:%s\n", status.StartTime, status.CompletionTime)
		if len(status.Conditions) > 0 {
			display.OutF("│   Conditions:\n")
		}
		for _, cond := range status.Conditions {
			display.OutF("│   - %s %s:%s", cond.LastTransitionTime, cond.Type, cond.Status)
			if cond.Reason != "" {
				display.OutF(" %s", cond.Reason)
			}
			if cond.Message != "" {
				display.OutF(" %s", cond.Message)
			}
			display.OutF("\n")
		}
	}

	for name := range mt.TrackingPods {
		if _, hasKey := mt.PodsStatuses[name]; hasKey {
			continue
		}
		display.OutF("├ po/%s status unavailable\n", name)
	}
	for name := range mt.TrackingDeployments {
		if _, hasKey := mt.DeploymentsStatuses[name]; hasKey {
			continue
		}
		display.OutF("├ deploy/%s status unavailable\n", name)
	}
	for name := range mt.TrackingStatefulSets {
		if _, hasKey := mt.StatefulSetsStatuses[name]; hasKey {
			continue
		}
		display.OutF("├ sts/%s status unavailable\n", name)
	}
	for name := range mt.TrackingDaemonSets {
		if _, hasKey := mt.DaemonSetsStatuses[name]; hasKey {
			continue
		}
		display.OutF("├ ds/%s status unavailable\n", name)
	}
	for name := range mt.TrackingJobs {
		if _, hasKey := mt.JobsStatuses[name]; hasKey {
			continue
		}
		display.OutF("├ job/%s status unavailable\n", name)
	}

	display.OutF("└ Status Report\n")

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
		return nil
	} else if spec.FailMode == IgnoreAndContinueDeployProcess {
		delete(resourcesStates, spec.ResourceName)
		return tracker.StopTrack
	} else {
		panic(fmt.Sprintf("bad fail mode: %s", spec.FailMode))
	}
}
