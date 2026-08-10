package generic

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/xeipuuv/gojsonschema"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/yaml"
)

//go:embed contrib_resource_status_rules.schema.json
var contribResourceStatusRulesSchema string

//go:embed contrib_resource_status_rules.yaml
var contribResourceStatusRules string

type ContribResourceStatusRules struct {
	Rules []struct {
		ResourceGroup *string  `yaml:"resourceGroup"`
		ResourceKind  *string  `yaml:"resourceKind"`
		JSONPaths     []string `yaml:"jsonPaths"`
		HumanJSONPath string   `yaml:"humanJsonPath"`
		Conditions    struct {
			Ready       []string `yaml:"ready"`
			Progressing []string `yaml:"progressing"`
			Failed      []string `yaml:"failed"`
		} `yaml:"conditions"`
	} `yaml:"rules"`
}

func buildContribResourceStatusRules() []*ResourceStatusJSONPathCondition {
	rulesJsonByte, err := yaml.ToJSON([]byte(contribResourceStatusRules))
	if err != nil {
		panic(fmt.Sprintf("convert rules yaml file to json: %s", err))
	}
	rulesJson := string(rulesJsonByte)

	schemaLoader := gojsonschema.NewStringLoader(contribResourceStatusRulesSchema)
	documentLoader := gojsonschema.NewStringLoader(rulesJson)

	if result, err := gojsonschema.Validate(schemaLoader, documentLoader); err != nil {
		panic(fmt.Sprintf("validate rules file: %s", err))
	} else if !result.Valid() {
		msg := "Rules file is not valid:\n"
		for _, err := range result.Errors() {
			msg += fmt.Sprintf("- %s\n", err)
		}
		panic(msg)
	}

	rules := &ContribResourceStatusRules{}
	if err := json.Unmarshal(rulesJsonByte, rules); err != nil {
		panic(fmt.Sprintf("unmarshal rules file: %s", err))
	}

	var conditions []*ResourceStatusJSONPathCondition
	for _, rule := range rules.Rules {
		var groupKind *schema.GroupKind
		var group *string
		if rule.ResourceGroup != nil && rule.ResourceKind != nil {
			groupKind = &schema.GroupKind{Group: *rule.ResourceGroup, Kind: *rule.ResourceKind}
		} else if rule.ResourceGroup != nil {
			group = rule.ResourceGroup
		}

		conditions = append(conditions, &ResourceStatusJSONPathCondition{
			GroupKind:     groupKind,
			Group:         group,
			JSONPaths:     rule.JSONPaths,
			HumanPath:     rule.HumanJSONPath,
			ReadyValues:   casify(rule.Conditions.Ready...),
			PendingValues: casify(rule.Conditions.Progressing...),
			FailedValues:  casify(rule.Conditions.Failed...),
		})
	}

	return conditions
}
