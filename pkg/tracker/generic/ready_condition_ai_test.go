//go:build ai_tests

package generic

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func managedServiceObject(kind string, conditions ...map[string]interface{}) *unstructured.Unstructured {
	object := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "managed-services.deckhouse.io/v1alpha1",
			"kind":       kind,
			"metadata":   map[string]interface{}{"name": "test"},
		},
	}

	if conditions != nil {
		rawConditions := make([]interface{}, 0, len(conditions))
		for _, c := range conditions {
			rawConditions = append(rawConditions, c)
		}

		object.Object["status"] = map[string]interface{}{
			"conditions": rawConditions,
		}
	}

	return object
}

func condition(conditionType, status string) map[string]interface{} {
	return map[string]interface{}{"type": conditionType, "status": status}
}

func TestAI_ManagedServiceReadyWhenBothConditionsTrue(t *testing.T) {
	object := managedServiceObject("Postgres", condition("Available", "True"), condition("LastValidConfigurationApplied", "True"))

	indicator, humanPath, err := NewResourceStatusIndicator(object)
	require.NoError(t, err)
	require.NotNil(t, indicator)

	assert.True(t, indicator.IsReady())
	assert.False(t, indicator.IsFailed())
	assert.Equal(t, "True, True", indicator.Value)
	assert.Equal(t, "status.conditions[type=Available&&LastValidConfigurationApplied].status", humanPath)
}

func TestAI_ManagedServiceNotReadyOnNonTrueCondition(t *testing.T) {
	for _, status := range []string{"False", "Unknown"} {
		for _, conditionType := range []string{"Available", "LastValidConfigurationApplied"} {
			t.Run(fmt.Sprintf("%s=%s", conditionType, status), func(t *testing.T) {
				conditions := []map[string]interface{}{
					condition("Available", "True"),
					condition("LastValidConfigurationApplied", "True"),
				}
				for i, c := range conditions {
					if c["type"] == conditionType {
						conditions[i] = condition(conditionType, status)
					}
				}

				indicator, _, err := NewResourceStatusIndicator(managedServiceObject("Postgres", conditions...))
				require.NoError(t, err)
				require.NotNil(t, indicator)

				assert.False(t, indicator.IsReady(), "must not be ready")
				assert.False(t, indicator.IsFailed(), "a non-True condition is progressing, never terminal")
			})
		}
	}
}

func TestAI_ManagedServicePendingWhenOneConditionMissing(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		conditions    []map[string]interface{}
		expectedValue string
	}{
		{
			name:          "LastValidConfigurationApplied missing",
			conditions:    []map[string]interface{}{condition("Available", "True")},
			expectedValue: "True, -",
		},
		{
			name:          "Available missing",
			conditions:    []map[string]interface{}{condition("LastValidConfigurationApplied", "True")},
			expectedValue: "-, True",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			indicator, _, err := NewResourceStatusIndicator(managedServiceObject("Postgres", testCase.conditions...))
			require.NoError(t, err)
			require.NotNil(t, indicator)

			assert.False(t, indicator.IsReady())
			assert.False(t, indicator.IsFailed())
			assert.Equal(t, testCase.expectedValue, indicator.Value)
		})
	}
}

func TestAI_NonWhitelistedKindsInGroupAreNotTracked(t *testing.T) {
	for _, kind := range []string{"PostgresClass", "ValkeyClass", "PostgresSnapshot", "SomeFutureKind"} {
		t.Run(kind, func(t *testing.T) {
			object := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "managed-services.deckhouse.io/v1alpha1",
					"kind":       kind,
					"metadata":   map[string]interface{}{"name": "test"},
					"status":     map[string]interface{}{},
				},
			}

			indicator, humanPath, err := NewResourceStatusIndicator(object)
			require.NoError(t, err)

			assert.Nil(t, indicator, "kinds outside the whitelist must not be tracked by the rule")
			assert.Empty(t, humanPath)
		})
	}
}

func TestAI_WhitelistedKindStaysPendingUntilConditionsAppear(t *testing.T) {
	object := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "managed-services.deckhouse.io/v1alpha1",
			"kind":       "Postgres",
			"metadata":   map[string]interface{}{"name": "test"},
			"status":     map[string]interface{}{},
		},
	}

	indicator, humanPath, err := NewResourceStatusIndicator(object)
	require.NoError(t, err)
	require.NotNil(t, indicator, "a whitelisted kind is claimed even before its conditions appear")

	assert.False(t, indicator.IsReady(), "must not go implicitly ready before the operator reports")
	assert.False(t, indicator.IsFailed())
	assert.Empty(t, indicator.Value)
	assert.Equal(t, "status.conditions[type=Available&&LastValidConfigurationApplied].status", humanPath)
}

func TestAI_EveryWhitelistedKindIsTracked(t *testing.T) {
	for _, kind := range managedServicesWhitelistedKinds {
		t.Run(kind, func(t *testing.T) {
			object := managedServiceObject(kind, condition("Available", "True"), condition("LastValidConfigurationApplied", "True"))

			indicator, _, err := NewResourceStatusIndicator(object)
			require.NoError(t, err)
			require.NotNil(t, indicator)

			assert.True(t, indicator.IsReady())
		})
	}
}

func TestAI_ManagedServiceReadyWithLowercaseConditionStatus(t *testing.T) {
	object := managedServiceObject("Postgres", condition("Available", "true"), condition("LastValidConfigurationApplied", "true"))

	indicator, _, err := NewResourceStatusIndicator(object)
	require.NoError(t, err)
	require.NotNil(t, indicator)

	assert.True(t, indicator.IsReady())
}

func TestAI_KindScopedRuleStaysPendingOnUnresolvedPath(t *testing.T) {
	object := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "postgresql.cnpg.io/v1",
			"kind":       "Cluster",
			"metadata":   map[string]interface{}{"name": "test"},
			"status":     map[string]interface{}{},
		},
	}

	indicator, humanPath, err := NewResourceStatusIndicator(object)
	require.NoError(t, err)
	require.NotNil(t, indicator, "an exact rule always claims its resource")

	assert.False(t, indicator.IsReady())
	assert.False(t, indicator.IsFailed())
	assert.Empty(t, indicator.Value, "an entirely unresolved rule must keep skipping the value display")
	assert.Equal(t, "status.conditions[type=Ready].status", humanPath)
}

func TestAI_UnrelatedResourceStillMatchedByHeuristics(t *testing.T) {
	object := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "example.com/v1",
			"kind":       "Widget",
			"metadata":   map[string]interface{}{"name": "test"},
			"status":     map[string]interface{}{"phase": "Ready"},
		},
	}

	indicator, humanPath, err := NewResourceStatusIndicator(object)
	require.NoError(t, err)
	require.NotNil(t, indicator)

	assert.True(t, indicator.IsReady())
	assert.Equal(t, "status.phase", humanPath)
}

func TestAI_FailedValueTakesPrecedenceOverReady(t *testing.T) {
	rule := &ResourceStatusJSONPathCondition{
		ReadyValues:  casify("True"),
		FailedValues: casify("False"),
	}

	indicator := newConditionIndicator(rule, []string{"True", "False"}, 2)

	assert.True(t, indicator.IsFailed(), "a failed value must win over the other path being ready")
	assert.False(t, indicator.IsReady())
	assert.Equal(t, "True, False", indicator.Value)
}

func TestAI_VerdictRequiresEveryPathReadyAndResolved(t *testing.T) {
	rule := &ResourceStatusJSONPathCondition{
		ReadyValues:   casify("True"),
		PendingValues: casify("False"),
	}

	for name, testCase := range map[string]struct {
		values        []string
		resolvedCount int
		expectedReady bool
		expectedValue string
	}{
		"every path ready":     {values: []string{"True", "True"}, resolvedCount: 2, expectedReady: true, expectedValue: "True, True"},
		"one path progressing": {values: []string{"True", "False"}, resolvedCount: 2, expectedReady: false, expectedValue: "True, False"},
		"one path unresolved":  {values: []string{"True", ""}, resolvedCount: 1, expectedReady: false, expectedValue: "True, -"},
		"all paths unresolved": {values: []string{"", ""}, resolvedCount: 0, expectedReady: false, expectedValue: ""},
	} {
		t.Run(name, func(t *testing.T) {
			indicator := newConditionIndicator(rule, testCase.values, testCase.resolvedCount)

			assert.Equal(t, testCase.expectedReady, indicator.IsReady())
			assert.False(t, indicator.IsFailed())
			assert.Equal(t, testCase.expectedValue, indicator.Value)
		})
	}
}

func TestAI_ConcurrentEvaluationIsDeterministic(t *testing.T) {
	ready := managedServiceObject("Postgres", condition("Available", "True"), condition("LastValidConfigurationApplied", "True"))
	pending := managedServiceObject("Valkey", condition("Available", "True"), condition("LastValidConfigurationApplied", "False"))

	for _, caseInsensitive := range []bool{false, true} {
		t.Run(fmt.Sprintf("caseInsensitive=%v", caseInsensitive), func(t *testing.T) {
			opts := NewResourceStatusIndicatorOptions{CaseInsensitiveConditionTracking: caseInsensitive}

			var wg sync.WaitGroup
			for i := 0; i < 500; i++ {
				wg.Add(2)

				go func() {
					defer wg.Done()

					indicator, _, err := NewResourceStatusIndicator(ready, opts)
					assert.NoError(t, err)
					if assert.NotNil(t, indicator) {
						assert.True(t, indicator.IsReady())
					}
				}()

				go func() {
					defer wg.Done()

					indicator, _, err := NewResourceStatusIndicator(pending, opts)
					assert.NoError(t, err)
					if assert.NotNil(t, indicator) {
						assert.False(t, indicator.IsReady())
					}
				}()
			}
			wg.Wait()
		})
	}
}
