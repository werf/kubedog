//go:build ai_tests

package generic

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xeipuuv/gojsonschema"
	"k8s.io/apimachinery/pkg/util/yaml"
)

type goldenRule struct {
	group       string
	kind        string
	jsonPath    string
	humanPath   string
	ready       []string
	progressing []string
	failed      []string
}

// Captured from the rule table as it stood before jsonPath became the jsonPaths list, so that
// the conversion cannot silently alter readiness detection for any already-supported resource.
var goldenContribRules = []goldenRule{
	{group: "acid.zalan.do", kind: "postgresql", jsonPath: "$.status.PostgresClusterStatus", humanPath: "status.PostgresClusterStatus", ready: []string{"Running"}, progressing: []string{"Creating", "Updating"}, failed: []string{"CreateFailed", "UpdateFailed", "DeleteFailed"}},
	{group: "external-secrets.io", kind: "ExternalSecret", jsonPath: "$.status.conditions[?(@.type==\"Ready\")].status", humanPath: "status.conditions[type=Ready].status", ready: []string{"True"}, progressing: []string{"Unknown"}, failed: []string{"False"}},
	{group: "bitnami.com", kind: "SealedSecret", jsonPath: "$.status.conditions[?(@.type=='Synced')].status", humanPath: "status.conditions[type=Synced].status", ready: []string{"True"}, progressing: []string{"Unknown", "False"}, failed: []string{}},
	{group: "kyverno.io", kind: "Policy", jsonPath: "$.status.conditions[?(@.type=='Ready')].status", humanPath: "status.conditions[type=Ready].status", ready: []string{"True"}, progressing: []string{"Unknown", "False"}, failed: []string{}},
	{group: "kyverno.io", kind: "ClusterPolicy", jsonPath: "$.status.conditions[?(@.type=='Ready')].status", humanPath: "status.conditions[type=Ready].status", ready: []string{"True"}, progressing: []string{"Unknown", "False"}, failed: []string{}},
	{group: "argoproj.io", kind: "Application", jsonPath: "$.status.health.status", humanPath: "status.health.status", ready: []string{"Healthy"}, progressing: []string{"Progressing", "Suspended", "Unknown", "Missing"}, failed: []string{"Degraded"}},
	{group: "argoproj.io", kind: "ApplicationSet", jsonPath: "$.status.conditions[?(@.type=='ResourcesUpToDate')].status", humanPath: "status.conditions[type=ResourcesUpToDate].status", ready: []string{"True"}, progressing: []string{"Unknown", "False"}, failed: []string{}},
	{group: "cert-manager.io", kind: "Certificate", jsonPath: "$.status.conditions[?(@.type==\"Ready\")].status", humanPath: "status.conditions[type=Ready].status", ready: []string{"True"}, progressing: []string{"Unknown", "False"}, failed: []string{}},
	{group: "helm.toolkit.fluxcd.io", kind: "HelmRelease", jsonPath: "$.status.conditions[?(@.type=='Ready')].status", humanPath: "status.conditions[type=Ready].status", ready: []string{"True"}, progressing: []string{"Unknown", "False"}, failed: []string{}},
	{group: "kustomize.toolkit.fluxcd.io", kind: "Kustomization", jsonPath: "$.status.conditions[?(@.type=='Ready')].status", humanPath: "status.conditions[type=Ready].status", ready: []string{"True"}, progressing: []string{"Unknown", "False"}, failed: []string{}},
	{group: "monitoring.coreos.com", kind: "Prometheus", jsonPath: "$.status.conditions[?(@.type=='Available')].status", humanPath: "status.conditions[type=Available].status", ready: []string{"True"}, progressing: []string{"Degraded", "Unknown", "False"}, failed: []string{}},
	{group: "monitoring.coreos.com", kind: "Alertmanager", jsonPath: "$.status.conditions[?(@.type=='Available')].status", humanPath: "status.conditions[type=Available].status", ready: []string{"True"}, progressing: []string{"Degraded", "Unknown", "False"}, failed: []string{}},
	{group: "monitoring.coreos.com", kind: "ThanosRuler", jsonPath: "$.status.conditions[?(@.type=='Available')].status", humanPath: "status.conditions[type=Available].status", ready: []string{"True"}, progressing: []string{"Degraded", "Unknown", "False"}, failed: []string{}},
	{group: "longhorn.io", kind: "Volume", jsonPath: "$.status.state", humanPath: "status.state", ready: []string{"attached", "detached"}, progressing: []string{"creating", "attaching", "detaching", "deleting"}, failed: []string{}},
	{group: "longhorn.io", kind: "Backup", jsonPath: "$.status.state", humanPath: "status.state", ready: []string{"Completed"}, progressing: []string{"Pending", "InProgress", "", "Unknown"}, failed: []string{"Error"}},
	{group: "postgresql.cnpg.io", kind: "Cluster", jsonPath: "$.status.conditions[?(@.type==\"Ready\")].status", humanPath: "status.conditions[type=Ready].status", ready: []string{"True"}, progressing: []string{"False", "Unknown"}, failed: []string{}},
}

func TestAI_ContribRulesPreserveExistingBehavior(t *testing.T) {
	rules := buildContribResourceStatusRules()

	require.GreaterOrEqual(t, len(rules), len(goldenContribRules))

	for i, golden := range goldenContribRules {
		rule := rules[i]

		require.NotNil(t, rule.GroupKind, "rule %d must stay kind-scoped", i)
		assert.Nil(t, rule.Group, "rule %d must not become group-only", i)
		assert.Equal(t, golden.group, rule.GroupKind.Group, "rule %d group", i)
		assert.Equal(t, golden.kind, rule.GroupKind.Kind, "rule %d kind", i)

		require.Len(t, rule.JSONPaths, 1, "rule %d must keep exactly one path", i)
		assert.Equal(t, golden.jsonPath, rule.JSONPaths[0], "rule %d path", i)
		assert.Equal(t, golden.humanPath, rule.HumanPath, "rule %d human path", i)

		assert.Equal(t, casify(golden.ready...), rule.ReadyValues, "rule %d ready values", i)
		assert.Equal(t, casify(golden.progressing...), rule.PendingValues, "rule %d pending values", i)
		assert.Equal(t, casify(golden.failed...), rule.FailedValues, "rule %d failed values", i)
	}
}

func TestAI_ManagedServicesRuleIsGroupOnly(t *testing.T) {
	rules := buildContribResourceStatusRules()

	rule := rules[len(rules)-1]

	require.Nil(t, rule.GroupKind)
	require.NotNil(t, rule.Group)
	assert.Equal(t, "managed-services.deckhouse.io", *rule.Group)
	assert.Equal(t, []string{
		`$.status.conditions[?(@.type=="Available")].status`,
		`$.status.conditions[?(@.type=="LastValidConfigurationApplied")].status`,
	}, rule.JSONPaths)
	assert.Equal(t, "status.conditions[type=Available|LastValidConfigurationApplied].status", rule.HumanPath)
	assert.Empty(t, rule.FailedValues)
}

func validateRuleDocument(t *testing.T, document string) *gojsonschema.Result {
	t.Helper()

	documentJSON, err := yaml.ToJSON([]byte(document))
	require.NoError(t, err)

	result, err := gojsonschema.Validate(
		gojsonschema.NewStringLoader(contribResourceStatusRulesSchema),
		gojsonschema.NewStringLoader(string(documentJSON)),
	)
	require.NoError(t, err)

	return result
}

func TestAI_SchemaAcceptsGroupOnlyAndGroupKindRules(t *testing.T) {
	for name, document := range map[string]string{
		"group only": `
rules:
  - resourceGroup: "managed-services.deckhouse.io"
    jsonPaths:
      - "$.status.conditions[?(@.type=='Available')].status"
    humanJsonPath: "status.conditions[type=Available].status"
    conditions:
      ready: ["True"]
      progressing: ["False"]
`,
		"group and kind": `
rules:
  - resourceGroup: "postgresql.cnpg.io"
    resourceKind: "Cluster"
    jsonPaths:
      - "$.status.conditions[?(@.type=='Ready')].status"
    humanJsonPath: "status.conditions[type=Ready].status"
    conditions:
      ready: ["True"]
      progressing: ["False"]
`,
	} {
		t.Run(name, func(t *testing.T) {
			result := validateRuleDocument(t, document)
			assert.True(t, result.Valid(), "expected valid, got: %v", result.Errors())
		})
	}
}

func TestAI_SchemaRejectsInvalidRules(t *testing.T) {
	for name, testCase := range map[string]struct {
		document      string
		expectedField string
	}{
		"empty path list": {
			document: `
rules:
  - resourceGroup: "managed-services.deckhouse.io"
    jsonPaths: []
    humanJsonPath: "status"
    conditions:
      ready: ["True"]
      progressing: ["False"]
`,
			expectedField: "jsonPaths",
		},
		"bare resource kind": {
			document: `
rules:
  - resourceKind: "Cluster"
    jsonPaths:
      - "$.status.phase"
    humanJsonPath: "status.phase"
    conditions:
      ready: ["True"]
      progressing: ["False"]
`,
			expectedField: "resourceGroup",
		},
		"legacy singular json path": {
			document: `
rules:
  - resourceGroup: "postgresql.cnpg.io"
    resourceKind: "Cluster"
    jsonPath: "$.status.phase"
    humanJsonPath: "status.phase"
    conditions:
      ready: ["True"]
      progressing: ["False"]
`,
			expectedField: "jsonPaths",
		},
	} {
		t.Run(name, func(t *testing.T) {
			result := validateRuleDocument(t, testCase.document)

			require.False(t, result.Valid())
			assert.Contains(t, result.Errors()[0].String(), testCase.expectedField)
		})
	}
}
