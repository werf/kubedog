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

func valueRules(table []*ResourceStatusJSONPathCondition) []*ResourceStatusJSONPathCondition {
	return lo.Filter(table, func(condition *ResourceStatusJSONPathCondition, _ int) bool {
		return condition.GroupKind == nil && !strings.Contains(condition.JSONPath, "status.conditions[?")
	})
}

func TestAI_CaseInsensitiveTableExpandsOnlyConditionRules(t *testing.T) {
	defaultTable := resourceStatusJSONPathConditions(false)
	variantTable := resourceStatusJSONPathConditions(true)

	// Compare by value, not by pointer: the two tables currently share the contrib and
	// low-priority sub-slices, so a pointer comparison would pass tautologically and would
	// not catch a future change that rebuilds those sections per table.
	byValue := func(conditions []*ResourceStatusJSONPathCondition) []ResourceStatusJSONPathCondition {
		return lo.Map(conditions, func(condition *ResourceStatusJSONPathCondition, _ int) ResourceStatusJSONPathCondition {
			return *condition
		})
	}

	exactDefault := lo.Filter(defaultTable, func(c *ResourceStatusJSONPathCondition, _ int) bool { return c.GroupKind != nil })
	exactVariant := lo.Filter(variantTable, func(c *ResourceStatusJSONPathCondition, _ int) bool { return c.GroupKind != nil })
	assert.Equal(t, byValue(exactDefault), byValue(exactVariant), "exact contrib rules must be identical in both tables")

	assert.Equal(t, byValue(valueRules(defaultTable)), byValue(valueRules(variantTable)),
		"the phase/state/health value rules and low-priority rules must be present, unchanged and in the same order in both tables")

	// Deliberately hardcoded: this is the tripwire that forces a human to look if the set of
	// ready condition types changes, since the variant-coverage test derives its expectations
	// from this same table and would otherwise silently accept the new set.
	assert.Len(t, conditionRules(defaultTable), 17, "one condition rule per ready type when case-insensitive matching is off")
	assert.Greater(t, len(conditionRules(variantTable)), len(conditionRules(defaultTable)))
}

func TestAI_CaseInsensitiveTableCoversEveryCasifyVariant(t *testing.T) {
	registered := lo.Map(conditionRules(resourceStatusJSONPathConditions(true)),
		func(condition *ResourceStatusJSONPathCondition, _ int) string { return condition.JSONPath })

	// Derive the expected types from the default table rather than hardcoding them, so a
	// ready type added to the builder is covered automatically instead of silently skipped.
	readyValues := lo.Map(conditionRules(resourceStatusJSONPathConditions(false)),
		func(condition *ResourceStatusJSONPathCondition, _ int) string {
			return strings.TrimSuffix(strings.TrimPrefix(condition.HumanPath, "status.conditions[type="), "].status")
		})
	require.NotEmpty(t, readyValues)

	for _, readyValue := range readyValues {
		for _, variant := range casify(readyValue) {
			expected := fmt.Sprintf(`$.status.conditions[?(@.type==%q)].status`, variant)
			assert.Contains(t, registered, expected, "missing rule for casing variant %q", variant)
		}
	}
}

func TestAI_CaseInsensitiveConditionTypesPreferCamelCase(t *testing.T) {
	assert.Equal(t, []string{"ready"}, conditionTypesByPriority("ready", false))

	variants := conditionTypesByPriority("ready", true)
	require.NotEmpty(t, variants)
	assert.Equal(t, caps.ToCamel("ready"), variants[0], "CamelCase must be emitted first so it wins first-match-wins")
	assert.ElementsMatch(t, casify("ready"), variants)
}

func TestAI_UniversalConditionTrackingRespectsOption(t *testing.T) {
	for _, tc := range []struct {
		name              string
		manifest          string
		readyWhenDisabled bool
		pathWhenDisabled  string
		readyWhenEnabled  bool
		pathWhenEnabled   string
	}{
		{
			name:              "CamelCase Ready=False",
			manifest:          widgetHeader + "status:\n  conditions:\n  - type: Ready\n    status: \"False\"\n",
			readyWhenDisabled: true,
			pathWhenDisabled:  "",
			readyWhenEnabled:  false,
			pathWhenEnabled:   "status.conditions[type=Ready].status",
		},
		{
			name:              "CamelCase Ready=True",
			manifest:          widgetHeader + "status:\n  conditions:\n  - type: Ready\n    status: \"True\"\n",
			readyWhenDisabled: true,
			pathWhenDisabled:  "",
			readyWhenEnabled:  true,
			pathWhenEnabled:   "status.conditions[type=Ready].status",
		},
		{
			name:              "CamelCase Available=False",
			manifest:          widgetHeader + "status:\n  conditions:\n  - type: Available\n    status: \"False\"\n",
			readyWhenDisabled: true,
			pathWhenDisabled:  "",
			readyWhenEnabled:  false,
			pathWhenEnabled:   "status.conditions[type=Available].status",
		},
		{
			name:              "lowercase ready=False works in both modes",
			manifest:          widgetHeader + "status:\n  conditions:\n  - type: ready\n    status: \"False\"\n",
			readyWhenDisabled: false,
			pathWhenDisabled:  "status.conditions[type=ready].status",
			readyWhenEnabled:  false,
			pathWhenEnabled:   "status.conditions[type=ready].status",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			disabled, err := NewResourceStatus(objectFromYAML(t, tc.manifest))
			require.NoError(t, err)
			assert.Equal(t, tc.readyWhenDisabled, disabled.IsReady())
			assert.Equal(t, tc.pathWhenDisabled, disabled.HumanConditionPath())

			enabled, err := NewResourceStatus(objectFromYAML(t, tc.manifest), NewResourceStatusOptions{
				CaseInsensitiveConditionTracking: true,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.readyWhenEnabled, enabled.IsReady())
			assert.Equal(t, tc.pathWhenEnabled, enabled.HumanConditionPath())
		})
	}
}

func TestAI_CaseInsensitiveTieBreakPrefersCamelCase(t *testing.T) {
	manifest := widgetHeader + "status:\n  conditions:\n  - type: Ready\n    status: \"True\"\n  - type: READY\n    status: \"False\"\n"

	status, err := NewResourceStatus(objectFromYAML(t, manifest), NewResourceStatusOptions{
		CaseInsensitiveConditionTracking: true,
	})
	require.NoError(t, err)

	assert.True(t, status.IsReady(), "distinct condition types must resolve via the CamelCase rule registered first")
	assert.Equal(t, "status.conditions[type=Ready].status", status.HumanConditionPath())
}

func TestAI_ValueRuleTrackingUnaffectedByOption(t *testing.T) {
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
			for _, caseInsensitive := range []bool{false, true} {
				status, err := NewResourceStatus(objectFromYAML(t, tc.manifest), NewResourceStatusOptions{
					CaseInsensitiveConditionTracking: caseInsensitive,
				})
				require.NoError(t, err)

				assert.Equal(t, tc.ready, status.IsReady(), "caseInsensitive=%v", caseInsensitive)
				assert.Equal(t, tc.path, status.HumanConditionPath(), "caseInsensitive=%v", caseInsensitive)
			}
		})
	}
}
