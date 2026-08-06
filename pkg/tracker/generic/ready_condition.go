package generic

import (
	"fmt"

	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/werf/kubedog/pkg/tracker/indicators"
	"github.com/werf/kubedog/pkg/utils"
)

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
	var matchedValue string
	for _, condition := range resourceStatusJSONPathConditions(opt.CaseInsensitiveConditionTracking) {
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
			matchedValue = currentValue
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
				matchedValue = currentValue
				break
			}
		}
	}

	if matchedCondition == nil {
		return nil, "", nil
	}

	indicator = &indicators.StringEqualConditionIndicator{
		Value: matchedValue,
	}
	indicator.SetReady(lo.Contains(matchedCondition.ReadyValues, matchedValue))
	indicator.SetFailed(lo.Contains(matchedCondition.FailedValues, matchedValue))

	return indicator, matchedCondition.HumanPath, nil
}
