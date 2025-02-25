package generic

import (
	"fmt"

	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/werf/kubedog/pkg/tracker/indicators"
	"github.com/werf/kubedog/pkg/utils"
)

func NewResourceStatusIndicator(object *unstructured.Unstructured) (indicator *indicators.StringEqualConditionIndicator, humanJSONPath string, err error) {
	groupKind := object.GroupVersionKind().GroupKind()

	var matchedCondition *ResourceStatusJSONPathCondition
	for _, condition := range ResourceStatusJSONPathConditions {
		exactCondition := condition.GroupKind != nil

		if exactCondition {
			exactMatch := *condition.GroupKind == groupKind
			if !exactMatch {
				continue
			}

			currentValue, _, err := utils.JSONPath(condition.JSONPath, object.UnstructuredContent())
			if err != nil {
				return nil, "", fmt.Errorf("jsonpath error: %w", err)
			}

			matchedCondition = condition
			matchedCondition.CurrentValue = currentValue
			break
		} else {
			currentValue, found, err := utils.JSONPath(condition.JSONPath, object.UnstructuredContent())
			if err != nil {
				return nil, "", fmt.Errorf("jsonpath error: %w", err)
			} else if !found {
				continue
			}

			knownValues := lo.Union(condition.ReadyValues, condition.PendingValues, condition.FailedValues)

			if lo.Contains(knownValues, currentValue) {
				matchedCondition = condition
				matchedCondition.CurrentValue = currentValue
				break
			}
		}
	}

	if matchedCondition == nil {
		return nil, "", nil
	}

	indicator = &indicators.StringEqualConditionIndicator{
		Value: matchedCondition.CurrentValue,
	}
	indicator.SetReady(lo.Contains(matchedCondition.ReadyValues, matchedCondition.CurrentValue))
	indicator.SetFailed(lo.Contains(matchedCondition.FailedValues, matchedCondition.CurrentValue))

	return indicator, matchedCondition.HumanPath, nil
}
