package generic

import (
	"fmt"
	"strings"

	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/werf/kubedog/pkg/tracker/indicators"
	"github.com/werf/kubedog/pkg/utils"
)

const unresolvedJSONPathValue = "-"

type NewResourceStatusIndicatorOptions struct {
	CaseInsensitiveConditionTracking bool
}

func NewResourceStatusIndicator(object *unstructured.Unstructured, opts ...NewResourceStatusIndicatorOptions) (indicator *indicators.StringEqualConditionIndicator, humanJSONPath string, err error) {
	var opt NewResourceStatusIndicatorOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	groupKind := object.GroupVersionKind().GroupKind()

	var matchedCondition *ResourceStatusJSONPathCondition
	var matchedValues []string
	var matchedResolvedCount int
	for _, condition := range resourceStatusJSONPathConditions(opt.CaseInsensitiveConditionTracking) {
		if condition.GroupKind != nil && *condition.GroupKind != groupKind {
			continue
		}

		if condition.Group != nil && *condition.Group != groupKind.Group {
			continue
		}

		values, resolvedCount, err := resolveConditionJSONPaths(condition, object)
		if err != nil {
			return nil, "", err
		}

		if condition.Group != nil && resolvedCount == 0 {
			continue
		}

		if condition.GroupKind == nil && condition.Group == nil {
			knownValues := lo.Union(condition.ReadyValues, condition.PendingValues, condition.FailedValues)

			if resolvedCount != len(values) || !lo.EveryBy(values, func(value string) bool {
				return lo.Contains(knownValues, value)
			}) {
				continue
			}
		}

		matchedCondition = condition
		matchedValues = values
		matchedResolvedCount = resolvedCount
		break
	}

	if matchedCondition == nil {
		return nil, "", nil
	}

	indicator = &indicators.StringEqualConditionIndicator{
		Value: formatConditionValues(matchedValues, matchedResolvedCount),
	}
	indicator.SetReady(matchedResolvedCount == len(matchedValues) && lo.EveryBy(matchedValues, func(value string) bool {
		return lo.Contains(matchedCondition.ReadyValues, value)
	}))
	indicator.SetFailed(lo.SomeBy(matchedValues, func(value string) bool {
		return lo.Contains(matchedCondition.FailedValues, value)
	}))

	return indicator, matchedCondition.HumanPath, nil
}

func resolveConditionJSONPaths(condition *ResourceStatusJSONPathCondition, object *unstructured.Unstructured) ([]string, int, error) {
	values := make([]string, len(condition.JSONPaths))

	var resolvedCount int
	for i, jsonPath := range condition.JSONPaths {
		value, found, err := utils.JSONPath(jsonPath, object.UnstructuredContent())
		if err != nil {
			return nil, 0, fmt.Errorf("jsonpath error: %w", err)
		}

		if !found {
			continue
		}

		values[i] = value
		resolvedCount++
	}

	return values, resolvedCount, nil
}

func formatConditionValues(values []string, resolvedCount int) string {
	if resolvedCount == 0 {
		return ""
	}

	if resolvedCount == len(values) {
		return strings.Join(values, ", ")
	}

	displayed := lo.Map(values, func(value string, _ int) string {
		return lo.Ternary(value == "", unresolvedJSONPathValue, value)
	})

	return strings.Join(displayed, ", ")
}
