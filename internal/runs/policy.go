package runs

import (
	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/problem"
)

type StartOptions struct {
	ExecuteAll    bool
	HistoryPolicy string
}

func NormalizeHistoryPolicy(value string) string {
	if value == "" {
		return "auto"
	}
	return value
}
func ValidHistoryPolicy(value string) bool {
	switch NormalizeHistoryPolicy(value) {
	case "auto", "retain", "unretained":
		return true
	}
	return false
}
func PolicyReasons(policy, status string, first, success bool) []string {
	if NormalizeHistoryPolicy(policy) == "unretained" {
		return []string{}
	}
	reasons := RetentionReasons(status, first, success)
	if policy == "retain" {
		reasons = append(reasons, "manual")
	}
	return reasons
}

func SelectStep(doc catalog.TestCase, stepID string) (catalog.TestCase, error) {
	if stepID == "" {
		return doc, nil
	}
	if !catalog.ValidID(stepID) {
		return doc, problem.Invalid("stepId", "Некорректный идентификатор шага")
	}
	for _, s := range doc.Steps {
		if s.ID == stepID {
			doc.Steps = []catalog.Step{s}
			return doc, nil
		}
	}
	return doc, problem.Invalid("stepId", "Шаг не относится к снимку прогона")
}
