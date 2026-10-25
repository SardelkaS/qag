package runs

import (
	"qag/internal/catalog"
	"qag/internal/execution"
	"qag/internal/resources"
	"time"
)

type StepState struct {
	StepID   string `json:"stepId"`
	Status   string `json:"status"`
	Comment  string `json:"comment"`
	Revision int64  `json:"revision"`
}

type Run struct {
	ID               string               `json:"id"`
	TestCaseID       string               `json:"testCaseId"`
	EnvironmentID    string               `json:"environmentId"`
	EnvironmentName  string               `json:"environmentName"`
	Status           string               `json:"status"`
	Snapshot         catalog.TestCase     `json:"snapshot"`
	Resources        []resources.Resource `json:"resources"`
	StepStates       []StepState          `json:"stepStates"`
	Attempts         []execution.Attempt  `json:"attempts"`
	RetentionReasons []string             `json:"retentionReasons"`
	StartedAt        time.Time            `json:"startedAt"`
	FinishedAt       *time.Time           `json:"finishedAt,omitempty"`
}
