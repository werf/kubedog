package statestore

import (
	"errors"

	domigraph "github.com/dominikbraun/graph"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/werf/kubedog/pkg/trackers/dyntracker/util"
	"github.com/werf/kubedog/pkg/trackers/rollout/multitrack"
)

type ReadinessTaskState struct {
	name             string
	namespace        string
	groupVersionKind schema.GroupVersionKind

	readyConditions   []ReadinessTaskConditionFn
	failureConditions []ReadinessTaskConditionFn

	resourceStatesTree domigraph.Graph[string, *util.Concurrent[*ResourceState]]
}

func NewReadinessTaskState(name, namespace string, groupVersionKind schema.GroupVersionKind, opts ReadinessTaskStateOptions) *ReadinessTaskState {
	totalAllowFailuresCount := opts.TotalAllowFailuresCount

	var failMode multitrack.FailMode
	if opts.FailMode != "" {
		failMode = opts.FailMode
	} else {
		failMode = multitrack.FailWholeDeployProcessImmediately
	}

	resourceStatesTree := domigraph.New[string, *util.Concurrent[*ResourceState]](func(s *util.Concurrent[*ResourceState]) string {
		var id string
		s.RTransaction(func(state *ResourceState) {
			id = state.ID()
		})
		return id
	}, domigraph.PreventCycles(), domigraph.Directed(), domigraph.Rooted())

	failureConditions := initReadinessTaskStateFailureConditions(failMode, totalAllowFailuresCount)
	readyConditions := initReadinessTaskStateReadyConditions()

	rootResourceState := util.NewConcurrent(NewResourceState(name, namespace, groupVersionKind))
	resourceStatesTree.AddVertex(rootResourceState)

	return &ReadinessTaskState{
		name:               name,
		namespace:          namespace,
		groupVersionKind:   groupVersionKind,
		failureConditions:  failureConditions,
		readyConditions:    readyConditions,
		resourceStatesTree: resourceStatesTree,
	}
}

type ReadinessTaskStateOptions struct {
	FailMode                multitrack.FailMode
	TotalAllowFailuresCount int
}

func (s *ReadinessTaskState) Name() string {
	return s.name
}

func (s *ReadinessTaskState) Namespace() string {
	return s.namespace
}

func (s *ReadinessTaskState) GroupVersionKind() schema.GroupVersionKind {
	return s.groupVersionKind
}

func (s *ReadinessTaskState) AddResourceState(name, namespace string, groupVersionKind schema.GroupVersionKind) {
	if _, err := s.resourceStatesTree.Vertex(util.ResourceID(name, namespace, groupVersionKind)); err != nil {
		if !errors.Is(err, domigraph.ErrVertexNotFound) {
			panic(err)
		}
	} else {
		return
	}

	resourceState := util.NewConcurrent(NewResourceState(name, namespace, groupVersionKind))
	lo.Must0(s.resourceStatesTree.AddVertex(resourceState))
}

func (s *ReadinessTaskState) AddDependency(fromName, fromNamespace string, fromGroupVersionKind schema.GroupVersionKind, toName, toNamespace string, toGroupVersionKind schema.GroupVersionKind) {
	if err := s.resourceStatesTree.AddEdge(util.ResourceID(fromName, fromNamespace, fromGroupVersionKind), util.ResourceID(toName, toNamespace, toGroupVersionKind)); err != nil {
		if !errors.Is(err, domigraph.ErrEdgeAlreadyExists) {
			panic(err)
		}
	}
}

func (s *ReadinessTaskState) ResourceState(name, namespace string, groupVersionKind schema.GroupVersionKind) *util.Concurrent[*ResourceState] {
	return lo.Must1(s.resourceStatesTree.Vertex(util.ResourceID(name, namespace, groupVersionKind)))
}

func (s *ReadinessTaskState) ResourceStates() []*util.Concurrent[*ResourceState] {
	var states []*util.Concurrent[*ResourceState]

	lo.Must0(domigraph.BFS(s.resourceStatesTree, util.ResourceID(s.name, s.namespace, s.groupVersionKind), func(id string) bool {
		state := lo.Must(s.resourceStatesTree.Vertex(id))
		states = append(states, state)
		return false
	}))

	return states
}

func (s *ReadinessTaskState) TraverseResourceStates(fromName, fromNamespace string, fromGroupVersionKind schema.GroupVersionKind) []*util.Concurrent[*ResourceState] {
	var states []*util.Concurrent[*ResourceState]

	lo.Must0(domigraph.BFS(s.resourceStatesTree, util.ResourceID(fromName, fromNamespace, fromGroupVersionKind), func(id string) bool {
		state := lo.Must(s.resourceStatesTree.Vertex(id))
		states = append(states, state)
		return false
	}))

	return states
}

func (s *ReadinessTaskState) Status() ReadinessTaskStatus {
	for _, failureCondition := range s.failureConditions {
		if failureCondition(s) {
			return ReadinessTaskStatusFailed
		}
	}

	for _, readyCondition := range s.readyConditions {
		if !readyCondition(s) {
			return ReadinessTaskStatusProgressing
		}
	}

	return ReadinessTaskStatusReady
}

func initReadinessTaskStateReadyConditions() []ReadinessTaskConditionFn {
	var readyConditions []ReadinessTaskConditionFn

	readyConditions = append(readyConditions, func(taskState *ReadinessTaskState) bool {
		resourcesReadyRequired := 1
		taskState.ResourceState(taskState.name, taskState.namespace, taskState.groupVersionKind).RTransaction(func(s *ResourceState) {
			if replicasAttr, found := lo.Find(s.Attributes(), func(attr Attributer) bool {
				return attr.Name() == AttributeNameRequiredReplicas
			}); found {
				resourcesReadyRequired += replicasAttr.(*Attribute[int]).Value
			}
		})

		var resourcesReady int
		lo.Must0(domigraph.BFS(taskState.resourceStatesTree, util.ResourceID(taskState.name, taskState.namespace, taskState.groupVersionKind), func(id string) bool {
			state := lo.Must(taskState.resourceStatesTree.Vertex(id))

			state.RTransaction(func(s *ResourceState) {
				if s.Status() == ResourceStatusReady {
					resourcesReady++
				}
			})

			return false
		}))

		return resourcesReady >= resourcesReadyRequired
	})

	return readyConditions
}

func initReadinessTaskStateFailureConditions(failMode multitrack.FailMode, totalAllowFailuresCount int) []ReadinessTaskConditionFn {
	var failureConditions []ReadinessTaskConditionFn

	failureConditions = append(failureConditions, func(taskState *ReadinessTaskState) bool {
		var failed bool
		lo.Must0(domigraph.BFS(taskState.resourceStatesTree, util.ResourceID(taskState.name, taskState.namespace, taskState.groupVersionKind), func(id string) bool {
			state := lo.Must(taskState.resourceStatesTree.Vertex(id))
			state.RTransaction(func(s *ResourceState) {
				if s.Status() == ResourceStatusFailed {
					failed = true
				}
			})

			if failed {
				return true
			}

			return false
		}))

		return failed
	})

	if failMode != multitrack.IgnoreAndContinueDeployProcess {
		maxErrors := lo.Max([]int{totalAllowFailuresCount + 1})

		failureConditions = append(failureConditions, func(taskState *ReadinessTaskState) bool {
			var totalErrsCount int
			lo.Must0(domigraph.BFS(taskState.resourceStatesTree, util.ResourceID(taskState.name, taskState.namespace, taskState.groupVersionKind), func(id string) bool {
				state := lo.Must(taskState.resourceStatesTree.Vertex(id))

				var errors map[string][]*Error
				state.RTransaction(func(s *ResourceState) {
					errors = s.Errors()
				})

				for _, errs := range errors {
					totalErrsCount += len(errs)
				}

				return false
			}))

			return totalErrsCount > maxErrors
		})
	}

	return failureConditions
}
