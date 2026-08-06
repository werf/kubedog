package generic

import (
	"fmt"
	"strings"

	"github.com/chanced/caps"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	ResourceStatusJSONPathConditions                []*ResourceStatusJSONPathCondition
	caseInsensitiveResourceStatusJSONPathConditions []*ResourceStatusJSONPathCondition
)

type ResourceStatusJSONPathCondition struct {
	GroupKind     *schema.GroupKind
	JSONPath      string
	HumanPath     string
	ReadyValues   []string
	PendingValues []string
	FailedValues  []string
}

func initResourceStatusJSONPathsByPriority() {
	contribRules := buildContribResourceStatusRules()
	lowPriorityConditions := buildLowPriorityConditions()

	ResourceStatusJSONPathConditions = lo.Flatten([][]*ResourceStatusJSONPathCondition{
		contribRules,
		buildUniversalConditions(false),
		lowPriorityConditions,
	})

	caseInsensitiveResourceStatusJSONPathConditions = lo.Flatten([][]*ResourceStatusJSONPathCondition{
		contribRules,
		buildUniversalConditions(true),
		lowPriorityConditions,
	})
}

func resourceStatusJSONPathConditions(caseInsensitiveConditionTracking bool) []*ResourceStatusJSONPathCondition {
	if caseInsensitiveConditionTracking {
		return caseInsensitiveResourceStatusJSONPathConditions
	}

	return ResourceStatusJSONPathConditions
}

func buildUniversalConditions(caseInsensitiveConditionTracking bool) []*ResourceStatusJSONPathCondition {
	var conditions []*ResourceStatusJSONPathCondition
	readyValuesByPriority := []string{
		"ready",
		"success",
		"succeeded",
		"complete",
		"completed",
		"finished",
		"finalized",
		"done",
		"available",
		"running",
		"ok",
		"active",
		"live",
		"healthy",
		"started",
		"initialized",
		"approved",
	}

	pendingValuesByPriority := []string{
		"creating",
		"updating",
		"waiting",
		"awaiting",
		"pending",
		"finishing",
		"starting",
		"readying",
		"in progress",
		"progressing",
		"initialization",
		"initializing",
		"approving",
		"unknown",
	}

	failedValuesByPriority := []string{
		"failure",
		"failed",
		"abort",
		"aborted",
		"terminated",
		"error",
		"errored",
		"rejection",
		"rejected",
	}

	for _, readyValue := range readyValuesByPriority {
		for _, conditionType := range conditionTypesByPriority(readyValue, caseInsensitiveConditionTracking) {
			conditions = append(conditions, &ResourceStatusJSONPathCondition{
				JSONPath:      fmt.Sprintf(`$.status.conditions[?(@.type==%q)].status`, conditionType),
				HumanPath:     fmt.Sprintf("status.conditions[type=%s].status", conditionType),
				ReadyValues:   casify("true"),
				PendingValues: casify("false", "unknown"),
			})
		}
	}

	conditions = append(conditions, &ResourceStatusJSONPathCondition{
		JSONPath:      `$.status.phase`,
		HumanPath:     "status.phase",
		ReadyValues:   casify(readyValuesByPriority...),
		PendingValues: casify(pendingValuesByPriority...),
		FailedValues:  casify(failedValuesByPriority...),
	})

	conditions = append(conditions, &ResourceStatusJSONPathCondition{
		JSONPath:      `$.status.currentPhase`,
		HumanPath:     "status.currentPhase",
		ReadyValues:   casify(readyValuesByPriority...),
		PendingValues: casify(pendingValuesByPriority...),
		FailedValues:  casify(failedValuesByPriority...),
	})

	conditions = append(conditions, &ResourceStatusJSONPathCondition{
		JSONPath:      `$.status.state`,
		HumanPath:     "status.state",
		ReadyValues:   casify(readyValuesByPriority...),
		PendingValues: casify(pendingValuesByPriority...),
		FailedValues:  casify(failedValuesByPriority...),
	})

	conditions = append(conditions, &ResourceStatusJSONPathCondition{
		JSONPath:      `$.status.currentState`,
		HumanPath:     "status.currentState",
		ReadyValues:   casify(readyValuesByPriority...),
		PendingValues: casify(pendingValuesByPriority...),
		FailedValues:  casify(failedValuesByPriority...),
	})

	conditions = append(conditions, &ResourceStatusJSONPathCondition{
		JSONPath:      `$.status.status`,
		HumanPath:     "status.status",
		ReadyValues:   casify(readyValuesByPriority...),
		PendingValues: casify(pendingValuesByPriority...),
		FailedValues:  casify(failedValuesByPriority...),
	})

	conditions = append(conditions, &ResourceStatusJSONPathCondition{
		JSONPath:      `$.status.currentStatus`,
		HumanPath:     "status.currentStatus",
		ReadyValues:   casify(readyValuesByPriority...),
		PendingValues: casify(pendingValuesByPriority...),
		FailedValues:  casify(failedValuesByPriority...),
	})

	conditions = append(conditions, &ResourceStatusJSONPathCondition{
		JSONPath:      `$.status.health`,
		HumanPath:     "status.health",
		ReadyValues:   casify(readyValuesByPriority...),
		PendingValues: casify(pendingValuesByPriority...),
		FailedValues:  casify(failedValuesByPriority...),
	})

	conditions = append(conditions, &ResourceStatusJSONPathCondition{
		JSONPath:      `$.status.currentHealth`,
		HumanPath:     "status.currentHealth",
		ReadyValues:   casify(readyValuesByPriority...),
		PendingValues: casify(pendingValuesByPriority...),
		FailedValues:  casify(failedValuesByPriority...),
	})

	return conditions
}

func buildLowPriorityConditions() []*ResourceStatusJSONPathCondition {
	var conditions []*ResourceStatusJSONPathCondition

	conditions = append(conditions, &ResourceStatusJSONPathCondition{
		JSONPath:      `$.status.state`,
		HumanPath:     "status.state",
		ReadyValues:   casify("valid"),
		PendingValues: casify("invalid", "unknown"),
	})

	conditions = append(conditions, &ResourceStatusJSONPathCondition{
		JSONPath:      `$.status.currentState`,
		HumanPath:     "status.currentState",
		ReadyValues:   casify("valid"),
		PendingValues: casify("invalid", "unknown"),
	})

	conditions = append(conditions, &ResourceStatusJSONPathCondition{
		JSONPath:      `$.status.health`,
		HumanPath:     "status.health",
		ReadyValues:   casify("green"),
		PendingValues: casify("yellow", "red", "unknown"),
	})

	conditions = append(conditions, &ResourceStatusJSONPathCondition{
		JSONPath:      `$.status.currentHealth`,
		HumanPath:     "status.currentHealth",
		ReadyValues:   casify("green"),
		PendingValues: casify("yellow", "red", "unknown"),
	})

	return conditions
}

func conditionTypesByPriority(readyValue string, caseInsensitiveConditionTracking bool) []string {
	if !caseInsensitiveConditionTracking {
		return []string{casify(readyValue)[0]}
	}

	return lo.Uniq(append([]string{caps.ToCamel(readyValue)}, casify(readyValue)...))
}

func casify(in ...string) []string {
	var result []string

	for _, value := range in {
		result = append(
			result,
			value,
			strings.ReplaceAll(value, " ", ""),
			caps.ToUpper(strings.ReplaceAll(value, " ", "")),
			caps.ToCamel(value),
			caps.ToKebab(value),
			caps.ToDotNotation(value),
			caps.ToSnake(value),
			caps.ToTitle(value),
			caps.ToUpper(value),
			caps.ToLower(value),
			caps.ToLowerCamel(value),
			caps.ToScreamingDotNotation(value),
			caps.ToScreamingKebab(value),
			caps.ToScreamingSnake(value),
		)
	}

	result = lo.Uniq(result)

	return result
}
