package generic

import (
	"fmt"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

var ResourceStatusJSONPathsByPriority []ResourceStatusJSONPath

type ResourceStatusJSONPath struct {
	JSONPath      string
	HumanPath     string
	ReadyValue    string
	FailedValue   string
	PendingValues []string
	CurrentValue  string
}

func initResourceStatusJSONPathsByPriority() {
	casers := []cases.Caser{cases.Lower(language.Und), cases.Title(language.Und)}

	for _, actionGroupsByPriority := range [][]string{
		{"ready", "success", "succeeded"},
		{"complete", "completed", "finished"},
		{"available"},
		{"running"},
		{"started", "initialized", "approved"},
	} {
		for _, action := range actionGroupsByPriority {
			for _, caser := range casers {
				ResourceStatusJSONPathsByPriority = append(ResourceStatusJSONPathsByPriority, ResourceStatusJSONPath{
					JSONPath:      fmt.Sprintf(`$.status.conditions[?(@.type==%q)].status`, caser.String(action)),
					HumanPath:     fmt.Sprintf("status.conditions[type=%s].status", caser.String(action)),
					ReadyValue:    caser.String("true"),
					PendingValues: []string{caser.String("false"), caser.String("unknown")},
				})

				ResourceStatusJSONPathsByPriority = append(ResourceStatusJSONPathsByPriority, ResourceStatusJSONPath{
					JSONPath:      `$.status.phase`,
					HumanPath:     "status.phase",
					ReadyValue:    caser.String(action),
					PendingValues: []string{caser.String("pending"), caser.String("unknown")},
					FailedValue:   caser.String("failed"),
				})

				ResourceStatusJSONPathsByPriority = append(ResourceStatusJSONPathsByPriority, ResourceStatusJSONPath{
					JSONPath:      `$.status.currentPhase`,
					HumanPath:     "status.currentPhase",
					ReadyValue:    caser.String(action),
					PendingValues: []string{caser.String("pending"), caser.String("unknown")},
					FailedValue:   caser.String("failed"),
				})

				ResourceStatusJSONPathsByPriority = append(ResourceStatusJSONPathsByPriority, ResourceStatusJSONPath{
					JSONPath:      `$.status.state`,
					HumanPath:     "status.state",
					ReadyValue:    caser.String(action),
					PendingValues: []string{caser.String("pending"), caser.String("unknown")},
					FailedValue:   caser.String("failed"),
				})

				ResourceStatusJSONPathsByPriority = append(ResourceStatusJSONPathsByPriority, ResourceStatusJSONPath{
					JSONPath:      `$.status.currentState`,
					HumanPath:     "status.currentState",
					ReadyValue:    caser.String(action),
					PendingValues: []string{caser.String("pending"), caser.String("unknown")},
					FailedValue:   caser.String("failed"),
				})

				ResourceStatusJSONPathsByPriority = append(ResourceStatusJSONPathsByPriority, ResourceStatusJSONPath{
					JSONPath:      `$.status.status`,
					HumanPath:     "status.status",
					ReadyValue:    caser.String(action),
					PendingValues: []string{caser.String("pending"), caser.String("unknown")},
					FailedValue:   caser.String("failed"),
				})

				ResourceStatusJSONPathsByPriority = append(ResourceStatusJSONPathsByPriority, ResourceStatusJSONPath{
					JSONPath:      `$.status.currentStatus`,
					HumanPath:     "status.currentStatus",
					ReadyValue:    caser.String(action),
					PendingValues: []string{caser.String("pending"), caser.String("unknown")},
					FailedValue:   caser.String("failed"),
				})

				ResourceStatusJSONPathsByPriority = append(ResourceStatusJSONPathsByPriority, ResourceStatusJSONPath{
					JSONPath:      `$.status.health`,
					HumanPath:     "status.health",
					ReadyValue:    caser.String(action),
					PendingValues: []string{caser.String("pending"), caser.String("unknown")},
					FailedValue:   caser.String("failed"),
				})

				ResourceStatusJSONPathsByPriority = append(ResourceStatusJSONPathsByPriority, ResourceStatusJSONPath{
					JSONPath:      `$.status.currentHealth`,
					HumanPath:     "status.currentHealth",
					ReadyValue:    caser.String(action),
					PendingValues: []string{caser.String("pending"), caser.String("unknown")},
					FailedValue:   caser.String("failed"),
				})
			}
		}
	}

	for _, caser := range casers {
		ResourceStatusJSONPathsByPriority = append(ResourceStatusJSONPathsByPriority, ResourceStatusJSONPath{
			JSONPath:      `$.status.state`,
			HumanPath:     "status.state",
			ReadyValue:    caser.String("valid"),
			PendingValues: []string{caser.String("invalid"), caser.String("unknown")},
		})

		ResourceStatusJSONPathsByPriority = append(ResourceStatusJSONPathsByPriority, ResourceStatusJSONPath{
			JSONPath:      `$.status.currentState`,
			HumanPath:     "status.currentState",
			ReadyValue:    caser.String("valid"),
			PendingValues: []string{caser.String("invalid"), caser.String("unknown")},
		})

		ResourceStatusJSONPathsByPriority = append(ResourceStatusJSONPathsByPriority, ResourceStatusJSONPath{
			JSONPath:      `$.status.health`,
			HumanPath:     "status.health",
			ReadyValue:    caser.String("green"),
			PendingValues: []string{caser.String("yellow"), caser.String("red"), caser.String("unknown")},
		})

		ResourceStatusJSONPathsByPriority = append(ResourceStatusJSONPathsByPriority, ResourceStatusJSONPath{
			JSONPath:      `$.status.currentHealth`,
			HumanPath:     "status.currentHealth",
			ReadyValue:    caser.String("green"),
			PendingValues: []string{caser.String("yellow"), caser.String("red"), caser.String("unknown")},
		})

	}
}
