package multitrack

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/flant/logboek"

	"k8s.io/client-go/kubernetes"

	"github.com/flant/kubedog/pkg/tracker"
	"github.com/flant/kubedog/pkg/tracker/daemonset"
	"github.com/flant/kubedog/pkg/tracker/deployment"
	"github.com/flant/kubedog/pkg/tracker/job"
	"github.com/flant/kubedog/pkg/tracker/statefulset"
)

type TrackTerminationMode string

const (
	WaitUntilResourceReady TrackTerminationMode = "WaitUntilResourceReady"
	NonBlocking            TrackTerminationMode = "NonBlocking"
)

type FailMode string

const (
	IgnoreAndContinueDeployProcess    FailMode = "IgnoreAndContinueDeployProcess"
	FailWholeDeployProcessImmediately FailMode = "FailWholeDeployProcessImmediately"
	HopeUntilEndOfDeployProcess       FailMode = "HopeUntilEndOfDeployProcess"
)

//type DeployCondition string
//
//const (
//	ControllerIsReady DeployCondition = "ControllerIsReady"
//	PodIsReady        DeployCondition = "PodIsReady"
//	EndOfDeploy       DeployCondition = "EndOfDeploy"
//)

var (
	ErrFailWholeDeployProcessImmediately = errors.New("fail whole deploy process immediately")
)

type MultitrackSpecs struct {
	Deployments  []MultitrackSpec
	StatefulSets []MultitrackSpec
	DaemonSets   []MultitrackSpec
	Jobs         []MultitrackSpec
}

type MultitrackSpec struct {
	ResourceName string
	Namespace    string

	TrackTerminationMode    TrackTerminationMode
	FailMode                FailMode
	AllowFailuresCount      *int
	FailureThresholdSeconds *int

	LogRegex                *regexp.Regexp
	LogRegexByContainerName map[string]*regexp.Regexp

	SkipLogs                  bool
	SkipLogsForContainers     []string
	ShowLogsOnlyForContainers []string
	//ShowLogsUntil             DeployCondition TODO

	ShowServiceMessages bool
}

type MultitrackOptions struct {
	tracker.Options
}

func newMultitrackOptions(parentContext context.Context, timeout time.Duration, logsFromTime time.Time) MultitrackOptions {
	return MultitrackOptions{Options: tracker.Options{ParentContext: parentContext, Timeout: timeout, LogsFromTime: logsFromTime}}
}

func setDefaultSpecValues(spec *MultitrackSpec) {
	if spec.TrackTerminationMode == "" {
		spec.TrackTerminationMode = WaitUntilResourceReady
	}

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
}

func Multitrack(kube kubernetes.Interface, specs MultitrackSpecs, opts MultitrackOptions) error {
	if len(specs.Deployments)+len(specs.StatefulSets)+len(specs.DaemonSets)+len(specs.Jobs) == 0 {
		return nil
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

	mt := multitracker{
		DeploymentsSpecs:        make(map[string]MultitrackSpec),
		DeploymentsContexts:     make(map[string]*multitrackerContext),
		TrackingDeployments:     make(map[string]*multitrackerResourceState),
		DeploymentsStatuses:     make(map[string]deployment.DeploymentStatus),
		PrevDeploymentsStatuses: make(map[string]deployment.DeploymentStatus),

		StatefulSetsSpecs:        make(map[string]MultitrackSpec),
		StatefulSetsContexts:     make(map[string]*multitrackerContext),
		TrackingStatefulSets:     make(map[string]*multitrackerResourceState),
		StatefulSetsStatuses:     make(map[string]statefulset.StatefulSetStatus),
		PrevStatefulSetsStatuses: make(map[string]statefulset.StatefulSetStatus),

		DaemonSetsSpecs:        make(map[string]MultitrackSpec),
		DaemonSetsContexts:     make(map[string]*multitrackerContext),
		TrackingDaemonSets:     make(map[string]*multitrackerResourceState),
		DaemonSetsStatuses:     make(map[string]daemonset.DaemonSetStatus),
		PrevDaemonSetsStatuses: make(map[string]daemonset.DaemonSetStatus),

		JobsSpecs:        make(map[string]MultitrackSpec),
		JobsContexts:     make(map[string]*multitrackerContext),
		TrackingJobs:     make(map[string]*multitrackerResourceState),
		JobsStatuses:     make(map[string]job.JobStatus),
		PrevJobsStatuses: make(map[string]job.JobStatus),

		serviceMessagesByResource: make(map[string][]string),
	}

	errorChan := make(chan error, 0)
	doneChan := make(chan struct{}, 0)
	statusProgressTicker := time.NewTicker(5 * time.Second)
	defer statusProgressTicker.Stop()

	doDisplayStatusProgress := func() error {
		mt.mux.Lock()
		defer mt.mux.Unlock()
		return mt.displayStatusProgress()
	}

	mt.Start(kube, specs, doneChan, errorChan, opts)

	for {
		select {
		case <-statusProgressTicker.C:
			if err := doDisplayStatusProgress(); err != nil {
				return err
			}

		case <-doneChan:
			return nil

		case err := <-errorChan:
			return err
		}
	}
}

func (mt *multitracker) Start(kube kubernetes.Interface, specs MultitrackSpecs, doneChan chan struct{}, errorChan chan error, opts MultitrackOptions) {
	mt.mux.Lock()
	defer mt.mux.Unlock()

	var wg sync.WaitGroup

	for _, spec := range specs.Deployments {
		mt.DeploymentsContexts[spec.ResourceName] = newMultitrackerContext(opts.ParentContext)
		mt.DeploymentsSpecs[spec.ResourceName] = spec
		mt.TrackingDeployments[spec.ResourceName] = newMultitrackerResourceState(spec)

		wg.Add(1)

		go mt.runSpecTracker("deploy", spec, &wg, mt.DeploymentsContexts, doneChan, errorChan, func(spec MultitrackSpec) error {
			return mt.TrackDeployment(kube, spec, newMultitrackOptions(mt.DeploymentsContexts[spec.ResourceName].Context, opts.Timeout, opts.LogsFromTime))
		})
	}

	for _, spec := range specs.StatefulSets {
		mt.StatefulSetsContexts[spec.ResourceName] = newMultitrackerContext(opts.ParentContext)
		mt.StatefulSetsSpecs[spec.ResourceName] = spec
		mt.TrackingStatefulSets[spec.ResourceName] = newMultitrackerResourceState(spec)

		wg.Add(1)

		go mt.runSpecTracker("sts", spec, &wg, mt.StatefulSetsContexts, doneChan, errorChan, func(spec MultitrackSpec) error {
			return mt.TrackStatefulSet(kube, spec, newMultitrackOptions(mt.StatefulSetsContexts[spec.ResourceName].Context, opts.Timeout, opts.LogsFromTime))
		})
	}

	for _, spec := range specs.DaemonSets {
		mt.DaemonSetsContexts[spec.ResourceName] = newMultitrackerContext(opts.ParentContext)
		mt.DaemonSetsSpecs[spec.ResourceName] = spec
		mt.TrackingDaemonSets[spec.ResourceName] = newMultitrackerResourceState(spec)

		wg.Add(1)

		go mt.runSpecTracker("ds", spec, &wg, mt.DaemonSetsContexts, doneChan, errorChan, func(spec MultitrackSpec) error {
			return mt.TrackDaemonSet(kube, spec, newMultitrackOptions(mt.DaemonSetsContexts[spec.ResourceName].Context, opts.Timeout, opts.LogsFromTime))
		})
	}

	for _, spec := range specs.Jobs {
		mt.JobsContexts[spec.ResourceName] = newMultitrackerContext(opts.ParentContext)
		mt.JobsSpecs[spec.ResourceName] = spec
		mt.TrackingJobs[spec.ResourceName] = newMultitrackerResourceState(spec)

		wg.Add(1)

		go mt.runSpecTracker("job", spec, &wg, mt.JobsContexts, doneChan, errorChan, func(spec MultitrackSpec) error {
			return mt.TrackJob(kube, spec, newMultitrackOptions(mt.JobsContexts[spec.ResourceName].Context, opts.Timeout, opts.LogsFromTime))
		})
	}

	if err := mt.applyTrackTerminationMode(); err != nil {
		errorChan <- fmt.Errorf("unable to apply termination mode: %s", err)
		return
	}

	go func() {
		wg.Wait()

		isAlreadyFailed := func() bool {
			mt.mux.Lock()
			defer mt.mux.Unlock()
			return mt.isFailed
		}()
		if isAlreadyFailed {
			return
		}

		err := func() error {
			mt.mux.Lock()
			defer mt.mux.Unlock()
			return mt.displayStatusProgress()
		}()
		if err != nil {
			errorChan <- err
			return
		}

		if mt.hasFailedTrackingResources() {
			mt.displayFailedTrackingResourcesServiceMessages()
			errorChan <- mt.formatFailedTrackingResourcesError()
		} else {
			doneChan <- struct{}{}
		}
	}()
}

func (mt *multitracker) applyTrackTerminationMode() error {
	if mt.isTerminating {
		return nil
	}

	shouldContinueTracking := func(name string, spec MultitrackSpec) bool {
		switch spec.TrackTerminationMode {
		case WaitUntilResourceReady:
			// There is at least one active context with wait mode,
			// so continue tracking without stopping any contexts
			return true

		case NonBlocking:
			return false

		default:
			panic(fmt.Sprintf("unknown TrackTerminationMode %#v", spec.TrackTerminationMode))
		}
	}

	var contextsToStop []*multitrackerContext

	for name, ctx := range mt.DeploymentsContexts {
		if shouldContinueTracking(name, mt.DeploymentsSpecs[name]) {
			return nil
		}
		contextsToStop = append(contextsToStop, ctx)
	}
	for name, ctx := range mt.StatefulSetsContexts {
		if shouldContinueTracking(name, mt.StatefulSetsSpecs[name]) {
			return nil
		}
		contextsToStop = append(contextsToStop, ctx)
	}
	for name, ctx := range mt.DaemonSetsContexts {
		if shouldContinueTracking(name, mt.DaemonSetsSpecs[name]) {
			return nil
		}
		contextsToStop = append(contextsToStop, ctx)
	}
	for name, ctx := range mt.JobsContexts {
		if shouldContinueTracking(name, mt.JobsSpecs[name]) {
			return nil
		}
		contextsToStop = append(contextsToStop, ctx)
	}

	mt.isTerminating = true

	for _, ctx := range contextsToStop {
		ctx.CancelFunc()
	}

	return nil
}

func (mt *multitracker) runSpecTracker(kind string, spec MultitrackSpec, wg *sync.WaitGroup, contexts map[string]*multitrackerContext, doneChan chan struct{}, errorChan chan error, trackerFunc func(MultitrackSpec) error) {
	defer wg.Done()

	err := trackerFunc(spec)

	mt.mux.Lock()
	defer mt.mux.Unlock()

	delete(contexts, spec.ResourceName)

	if err == ErrFailWholeDeployProcessImmediately {
		mt.displayFailedTrackingResourcesServiceMessages()
		errorChan <- mt.formatFailedTrackingResourcesError()
		mt.isFailed = true
		return
	} else if err == context.Canceled {
		return
	} else if err != nil {
		// unknown error
		errorChan <- fmt.Errorf("%s/%s track failed: %s", kind, spec.ResourceName, err)
		mt.isFailed = true
		return
	}

	if err := mt.applyTrackTerminationMode(); err != nil {
		errorChan <- fmt.Errorf("unable to apply termination mode: %s", err)
		mt.isFailed = true
		return
	}
}

type multitracker struct {
	DeploymentsSpecs        map[string]MultitrackSpec
	DeploymentsContexts     map[string]*multitrackerContext
	TrackingDeployments     map[string]*multitrackerResourceState
	DeploymentsStatuses     map[string]deployment.DeploymentStatus
	PrevDeploymentsStatuses map[string]deployment.DeploymentStatus

	StatefulSetsSpecs        map[string]MultitrackSpec
	StatefulSetsContexts     map[string]*multitrackerContext
	TrackingStatefulSets     map[string]*multitrackerResourceState
	StatefulSetsStatuses     map[string]statefulset.StatefulSetStatus
	PrevStatefulSetsStatuses map[string]statefulset.StatefulSetStatus

	DaemonSetsSpecs        map[string]MultitrackSpec
	DaemonSetsContexts     map[string]*multitrackerContext
	TrackingDaemonSets     map[string]*multitrackerResourceState
	DaemonSetsStatuses     map[string]daemonset.DaemonSetStatus
	PrevDaemonSetsStatuses map[string]daemonset.DaemonSetStatus

	JobsSpecs        map[string]MultitrackSpec
	JobsContexts     map[string]*multitrackerContext
	TrackingJobs     map[string]*multitrackerResourceState
	JobsStatuses     map[string]job.JobStatus
	PrevJobsStatuses map[string]job.JobStatus

	mux sync.Mutex

	isFailed      bool
	isTerminating bool

	displayCalled             bool
	currentLogProcessHeader   string
	currentLogProcessOptions  logboek.LogProcessStartOptions
	serviceMessagesByResource map[string][]string
}

type multitrackerContext struct {
	Context    context.Context
	CancelFunc context.CancelFunc
}

func newMultitrackerContext(parentContext context.Context) *multitrackerContext {
	if parentContext == nil {
		parentContext = context.Background()
	}
	ctx, cancel := context.WithCancel(parentContext)
	return &multitrackerContext{Context: ctx, CancelFunc: cancel}
}

type multitrackerResourceStatus string

const (
	resourceActive            multitrackerResourceStatus = "resourceActive"
	resourceSucceeded         multitrackerResourceStatus = "resourceSucceeded"
	resourceFailed            multitrackerResourceStatus = "resourceFailed"
	resourceHoping            multitrackerResourceStatus = "resourceHoping"
	resourceActiveAfterHoping multitrackerResourceStatus = "resourceActiveAfterHoping"
)

type multitrackerResourceState struct {
	Status                   multitrackerResourceStatus
	FailedReason             string
	FailuresCount            int
	FailuresCountAfterHoping int
}

func newMultitrackerResourceState(spec MultitrackSpec) *multitrackerResourceState {
	return &multitrackerResourceState{Status: resourceActive}
}

func (mt *multitracker) hasFailedTrackingResources() bool {
	for _, states := range []map[string]*multitrackerResourceState{
		mt.TrackingDeployments,
		mt.TrackingStatefulSets,
		mt.TrackingDaemonSets,
		mt.TrackingJobs,
	} {
		for _, state := range states {
			if state.Status == resourceFailed {
				return true
			}
		}
	}
	return false
}

func (mt *multitracker) formatFailedTrackingResourcesError() error {
	msgParts := []string{}

	for name, state := range mt.TrackingDeployments {
		if state.Status != resourceFailed {
			continue
		}
		msgParts = append(msgParts, fmt.Sprintf("deploy/%s failed: %s", name, state.FailedReason))
	}
	for name, state := range mt.TrackingStatefulSets {
		if state.Status != resourceFailed {
			continue
		}
		msgParts = append(msgParts, fmt.Sprintf("sts/%s failed: %s", name, state.FailedReason))
	}
	for name, state := range mt.TrackingDaemonSets {
		if state.Status != resourceFailed {
			continue
		}
		msgParts = append(msgParts, fmt.Sprintf("ds/%s failed: %s", name, state.FailedReason))
	}
	for name, state := range mt.TrackingJobs {
		if state.Status != resourceFailed {
			continue
		}
		msgParts = append(msgParts, fmt.Sprintf("job/%s failed: %s", name, state.FailedReason))
	}

	return fmt.Errorf("%s", strings.Join(msgParts, "\n"))
}

func (mt *multitracker) handleResourceReadyCondition(resourcesStates map[string]*multitrackerResourceState, spec MultitrackSpec) error {
	resourcesStates[spec.ResourceName].Status = resourceSucceeded
	return tracker.StopTrack
}

func (mt *multitracker) handleResourceFailure(resourcesStates map[string]*multitrackerResourceState, kind string, spec MultitrackSpec, reason string) error {
	switch spec.FailMode {
	case FailWholeDeployProcessImmediately:
		resourcesStates[spec.ResourceName].FailuresCount++

		if resourcesStates[spec.ResourceName].FailuresCount <= *spec.AllowFailuresCount {
			mt.displayMultitrackServiceMessageF("%d out of %d allowed errors occurred for %s/%s\n", resourcesStates[spec.ResourceName].FailuresCount, *spec.AllowFailuresCount, kind, spec.ResourceName)
			return nil
		}

		mt.displayMultitrackServiceMessageF("Allowed failures count for %s/%s exceeded %d errors: stop tracking immediately!\n", kind, spec.ResourceName, *spec.AllowFailuresCount)

		resourcesStates[spec.ResourceName].Status = resourceFailed
		resourcesStates[spec.ResourceName].FailedReason = reason

		return ErrFailWholeDeployProcessImmediately

	case HopeUntilEndOfDeployProcess:

	handleResourceState:
		switch resourcesStates[spec.ResourceName].Status {
		case resourceActive:
			resourcesStates[spec.ResourceName].Status = resourceHoping
			goto handleResourceState

		case resourceHoping:
			activeResourcesNames := mt.getActiveResourcesNames()
			if len(activeResourcesNames) > 0 {
				mt.displayMultitrackServiceMessageF("Error occurred for %s/%s, waiting until following resources are ready before counting errors (HopeUntilEndOfDeployProcess fail mode is active): %s\n", kind, spec.ResourceName, strings.Join(activeResourcesNames, ", "))
				return nil
			}

			resourcesStates[spec.ResourceName].Status = resourceActiveAfterHoping
			goto handleResourceState

		case resourceActiveAfterHoping:
			resourcesStates[spec.ResourceName].FailuresCount++

			if resourcesStates[spec.ResourceName].FailuresCount <= *spec.AllowFailuresCount {
				mt.displayMultitrackServiceMessageF("%d out of %d allowed errors occurred for %s/%s\n", resourcesStates[spec.ResourceName].FailuresCount, *spec.AllowFailuresCount, kind, spec.ResourceName)
				return nil
			}

			mt.displayMultitrackServiceMessageF("Allowed failures count for %s/%s exceeded %d errors: stop tracking immediately!\n", kind, spec.ResourceName, *spec.AllowFailuresCount)

			resourcesStates[spec.ResourceName].Status = resourceFailed
			resourcesStates[spec.ResourceName].FailedReason = reason

			return ErrFailWholeDeployProcessImmediately

		default:
			panic(fmt.Sprintf("%s/%s tracker is in unexpected state %#v", kind, spec.ResourceName, resourcesStates[spec.ResourceName].Status))
		}

	case IgnoreAndContinueDeployProcess:
		resourcesStates[spec.ResourceName].FailuresCount++
		mt.displayMultitrackServiceMessageF("%d errors occurred for %s/%s\n", resourcesStates[spec.ResourceName].FailuresCount, kind, spec.ResourceName)
		return nil

	default:
		panic(fmt.Sprintf("bad fail mode %#v for resource %s/%s", spec.FailMode, kind, spec.ResourceName))
	}

	return nil
}

func (mt *multitracker) getActiveResourcesNames() []string {
	activeResources := []string{}

	for name, state := range mt.TrackingDeployments {
		if state.Status == resourceActive {
			activeResources = append(activeResources, fmt.Sprintf("deploy/%s", name))
		}
	}
	for name, state := range mt.TrackingStatefulSets {
		if state.Status == resourceActive {
			activeResources = append(activeResources, fmt.Sprintf("sts/%s", name))
		}
	}
	for name, state := range mt.TrackingDaemonSets {
		if state.Status == resourceActive {
			activeResources = append(activeResources, fmt.Sprintf("ds/%s", name))
		}
	}
	for name, state := range mt.TrackingJobs {
		if state.Status == resourceActive {
			activeResources = append(activeResources, fmt.Sprintf("job/%s", name))
		}
	}

	return activeResources
}
