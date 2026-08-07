//go:build ai_tests

package generic

import (
	"fmt"
	"strings"
	"testing"

	"github.com/chanced/caps"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const widgetHeader = "apiVersion: example.io/v1\nkind: Widget\nmetadata:\n  name: w\n"

func conditionRules(table []*ResourceStatusJSONPathCondition) []*ResourceStatusJSONPathCondition {
	return lo.Filter(table, func(condition *ResourceStatusJSONPathCondition, _ int) bool {
		return condition.GroupKind == nil && strings.Contains(condition.JSONPath, "status.conditions[?")
	})
}

func TestAI_ConditionTableCoversEveryCasifyVariant(t *testing.T) {
	rules := conditionRules(ResourceStatusJSONPathConditions)

	registered := lo.Map(rules, func(condition *ResourceStatusJSONPathCondition, _ int) string { return condition.JSONPath })

	conditionTypes := lo.Map(rules, func(condition *ResourceStatusJSONPathCondition, _ int) string {
		return strings.TrimSuffix(strings.TrimPrefix(condition.HumanPath, "status.conditions[type="), "].status")
	})

	// Derive the ready types from the table rather than hardcoding them, so a ready type added
	// to the builder is covered automatically. Every ready value is a single lowercase word and
	// casify emits it unchanged, so the lowercase types are exactly the ready values.
	readyValues := lo.Filter(conditionTypes, func(conditionType string, _ int) bool {
		return conditionType == strings.ToLower(conditionType)
	})

	// Hardcoded on purpose: a tripwire forcing a human to look if the set of ready types changes,
	// since the coverage expectations below are derived from that same set.
	require.Len(t, readyValues, 17)

	for _, readyValue := range readyValues {
		for _, variant := range casify(readyValue) {
			expected := fmt.Sprintf(`$.status.conditions[?(@.type==%q)].status`, variant)
			assert.Contains(t, registered, expected, "missing rule for casing variant %q", variant)
		}
	}
}

func TestAI_CaseInsensitiveConditionTypesPreferCamelCase(t *testing.T) {
	variants := conditionTypesByPriority("ready")

	require.NotEmpty(t, variants)
	assert.Equal(t, caps.ToCamel("ready"), variants[0], "CamelCase must be emitted first so it wins first-match-wins")
	assert.ElementsMatch(t, casify("ready"), variants)
}

func TestAI_UniversalConditionTracking(t *testing.T) {
	for _, tc := range []struct {
		name     string
		manifest string
		ready    bool
		path     string
	}{
		{
			name:     "CamelCase Ready=False",
			manifest: widgetHeader + "status:\n  conditions:\n  - type: Ready\n    status: \"False\"\n",
			ready:    false,
			path:     "status.conditions[type=Ready].status",
		},
		{
			name:     "CamelCase Ready=True",
			manifest: widgetHeader + "status:\n  conditions:\n  - type: Ready\n    status: \"True\"\n",
			ready:    true,
			path:     "status.conditions[type=Ready].status",
		},
		{
			name:     "CamelCase Available=False",
			manifest: widgetHeader + "status:\n  conditions:\n  - type: Available\n    status: \"False\"\n",
			ready:    false,
			path:     "status.conditions[type=Available].status",
		},
		{
			name:     "lowercase ready=False",
			manifest: widgetHeader + "status:\n  conditions:\n  - type: ready\n    status: \"False\"\n",
			ready:    false,
			path:     "status.conditions[type=ready].status",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, err := NewResourceStatus(objectFromYAML(t, tc.manifest))
			require.NoError(t, err)

			assert.Equal(t, tc.ready, status.IsReady())
			assert.Equal(t, tc.path, status.HumanConditionPath())
		})
	}
}

func TestAI_CaseInsensitiveTieBreakPrefersCamelCase(t *testing.T) {
	manifest := widgetHeader + "status:\n  conditions:\n  - type: Ready\n    status: \"True\"\n  - type: READY\n    status: \"False\"\n"

	status, err := NewResourceStatus(objectFromYAML(t, manifest))
	require.NoError(t, err)

	assert.True(t, status.IsReady(), "distinct condition types must resolve via the CamelCase rule registered first")
	assert.Equal(t, "status.conditions[type=Ready].status", status.HumanConditionPath())
}

func TestAI_ValueRuleTracking(t *testing.T) {
	for _, tc := range []struct {
		name     string
		manifest string
		ready    bool
		path     string
	}{
		{"phase=Running", widgetHeader + "status:\n  phase: Running\n", true, "status.phase"},
		{"phase=Pending", widgetHeader + "status:\n  phase: Pending\n", false, "status.phase"},
		{"phase=Failed", widgetHeader + "status:\n  phase: Failed\n", false, "status.phase"},
		{"health=green (low priority rule)", widgetHeader + "status:\n  health: green\n", true, "status.health"},
		{"health=red (low priority rule)", widgetHeader + "status:\n  health: red\n", false, "status.health"},
		{"state=valid (low priority rule)", widgetHeader + "status:\n  state: valid\n", true, "status.state"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, err := NewResourceStatus(objectFromYAML(t, tc.manifest))
			require.NoError(t, err)

			assert.Equal(t, tc.ready, status.IsReady())
			assert.Equal(t, tc.path, status.HumanConditionPath())
		})
	}
}
