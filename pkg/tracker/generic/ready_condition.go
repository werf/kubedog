package generic

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/werf/kubedog/pkg/tracker/indicators"
	"github.com/werf/kubedog/pkg/utils"
)

func NewResourceStatusIndicator(object *unstructured.Unstructured) (indicator *indicators.StringEqualConditionIndicator, humanJSONPath string, err error) {
	var matchedJSONPath *ResourceStatusJSONPath
	for _, readyJSONPath := range ResourceStatusJSONPathsByPriority {
		result, found, err := utils.JSONPath(readyJSONPath.JSONPath, object.UnstructuredContent())
		if err != nil {
			return nil, "", fmt.Errorf("jsonpath error: %w", err)
		} else if !found {
			continue
		}

		var resultIsValidValue bool
		for _, validValue := range append(readyJSONPath.PendingValues, readyJSONPath.ReadyValue, readyJSONPath.FailedValue) {
			if result == validValue {
				resultIsValidValue = true
				break
			}
		}
		if !resultIsValidValue {
			continue
		}

		path := readyJSONPath
		matchedJSONPath = &path
		matchedJSONPath.CurrentValue = result

		break
	}

	if matchedJSONPath == nil {
		return nil, "", nil
	}

	return &indicators.StringEqualConditionIndicator{
		Value:       matchedJSONPath.CurrentValue,
		TargetValue: matchedJSONPath.ReadyValue,
		FailedValue: matchedJSONPath.FailedValue,
	}, matchedJSONPath.HumanPath, nil
}
