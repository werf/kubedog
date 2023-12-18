package generic

import (
	"fmt"

	"github.com/samber/lo"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var ResourceStatusJSONPathConditions []*ResourceStatusJSONPathCondition

type ResourceStatusJSONPathCondition struct {
	GroupKind     *schema.GroupKind
	JSONPath      string
	HumanPath     string
	ReadyValues   []string
	PendingValues []string
	FailedValues  []string

	CurrentValue string
}

func initResourceStatusJSONPathsByPriority() {
	genericReadyValuesByPriority := []string{
		"ready",
		"success",
		"succeeded",
		"complete",
		"completed",
		"finished",
		"available",
		"running",
		"started",
		"initialized",
		"approved",
	}

	genericPendingValuesByPriority := []string{
		"pending",
		"unknown",
	}

	genericFailedValuesByPriority := []string{
		"failed",
	}

	for _, readyValue := range genericReadyValuesByPriority {
		ResourceStatusJSONPathConditions = append(ResourceStatusJSONPathConditions, &ResourceStatusJSONPathCondition{
			JSONPath:      fmt.Sprintf(`$.status.conditions[?(@.type==%q)].status`, casify(readyValue)[0]),
			HumanPath:     fmt.Sprintf("status.conditions[type=%s].status", casify(readyValue)[0]),
			ReadyValues:   casify("true"),
			PendingValues: casify("false", "unknown"),
		})
	}

	ResourceStatusJSONPathConditions = append(ResourceStatusJSONPathConditions, &ResourceStatusJSONPathCondition{
		JSONPath:      `$.status.phase`,
		HumanPath:     "status.phase",
		ReadyValues:   casify(genericReadyValuesByPriority...),
		PendingValues: casify(genericPendingValuesByPriority...),
		FailedValues:  casify(genericFailedValuesByPriority...),
	})

	ResourceStatusJSONPathConditions = append(ResourceStatusJSONPathConditions, &ResourceStatusJSONPathCondition{
		JSONPath:      `$.status.currentPhase`,
		HumanPath:     "status.currentPhase",
		ReadyValues:   casify(genericReadyValuesByPriority...),
		PendingValues: casify(genericPendingValuesByPriority...),
		FailedValues:  casify(genericFailedValuesByPriority...),
	})

	ResourceStatusJSONPathConditions = append(ResourceStatusJSONPathConditions, &ResourceStatusJSONPathCondition{
		JSONPath:      `$.status.state`,
		HumanPath:     "status.state",
		ReadyValues:   casify(genericReadyValuesByPriority...),
		PendingValues: casify(genericPendingValuesByPriority...),
		FailedValues:  casify(genericFailedValuesByPriority...),
	})

	ResourceStatusJSONPathConditions = append(ResourceStatusJSONPathConditions, &ResourceStatusJSONPathCondition{
		JSONPath:      `$.status.currentState`,
		HumanPath:     "status.currentState",
		ReadyValues:   casify(genericReadyValuesByPriority...),
		PendingValues: casify(genericPendingValuesByPriority...),
		FailedValues:  casify(genericFailedValuesByPriority...),
	})

	ResourceStatusJSONPathConditions = append(ResourceStatusJSONPathConditions, &ResourceStatusJSONPathCondition{
		JSONPath:      `$.status.status`,
		HumanPath:     "status.status",
		ReadyValues:   casify(genericReadyValuesByPriority...),
		PendingValues: casify(genericPendingValuesByPriority...),
		FailedValues:  casify(genericFailedValuesByPriority...),
	})

	ResourceStatusJSONPathConditions = append(ResourceStatusJSONPathConditions, &ResourceStatusJSONPathCondition{
		JSONPath:      `$.status.currentStatus`,
		HumanPath:     "status.currentStatus",
		ReadyValues:   casify(genericReadyValuesByPriority...),
		PendingValues: casify(genericPendingValuesByPriority...),
		FailedValues:  casify(genericFailedValuesByPriority...),
	})

	ResourceStatusJSONPathConditions = append(ResourceStatusJSONPathConditions, &ResourceStatusJSONPathCondition{
		JSONPath:      `$.status.health`,
		HumanPath:     "status.health",
		ReadyValues:   casify(genericReadyValuesByPriority...),
		PendingValues: casify(genericPendingValuesByPriority...),
		FailedValues:  casify(genericFailedValuesByPriority...),
	})

	ResourceStatusJSONPathConditions = append(ResourceStatusJSONPathConditions, &ResourceStatusJSONPathCondition{
		JSONPath:      `$.status.currentHealth`,
		HumanPath:     "status.currentHealth",
		ReadyValues:   casify(genericReadyValuesByPriority...),
		PendingValues: casify(genericPendingValuesByPriority...),
		FailedValues:  casify(genericFailedValuesByPriority...),
	})

	ResourceStatusJSONPathConditions = append(ResourceStatusJSONPathConditions, &ResourceStatusJSONPathCondition{
		JSONPath:      `$.status.state`,
		HumanPath:     "status.state",
		ReadyValues:   casify("valid"),
		PendingValues: casify("invalid", "unknown"),
	})

	ResourceStatusJSONPathConditions = append(ResourceStatusJSONPathConditions, &ResourceStatusJSONPathCondition{
		JSONPath:      `$.status.currentState`,
		HumanPath:     "status.currentState",
		ReadyValues:   casify("valid"),
		PendingValues: casify("invalid", "unknown"),
	})

	ResourceStatusJSONPathConditions = append(ResourceStatusJSONPathConditions, &ResourceStatusJSONPathCondition{
		JSONPath:      `$.status.health`,
		HumanPath:     "status.health",
		ReadyValues:   casify("green"),
		PendingValues: casify("yellow", "red", "unknown"),
	})

	ResourceStatusJSONPathConditions = append(ResourceStatusJSONPathConditions, &ResourceStatusJSONPathCondition{
		JSONPath:      `$.status.currentHealth`,
		HumanPath:     "status.currentHealth",
		ReadyValues:   casify("green"),
		PendingValues: casify("yellow", "red", "unknown"),
	})
}

func casify(in ...string) []string {
	var result []string

	casers := []cases.Caser{cases.Lower(language.Und), cases.Title(language.Und)}
	for _, value := range in {
		result = append(result, value)

		for _, caser := range casers {
			cased := caser.String(value)

			if lo.Contains(result, cased) {
				continue
			}

			result = append(result, caser.String(value))
		}
	}

	return result
}
