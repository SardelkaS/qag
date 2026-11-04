package runs

import (
	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/execution"
	"github.com/SardelkaS/qag/internal/resources"
	"time"
)

type StepState struct {
	LegacyStatus string `json:"legacyStatus,omitempty"`
	StepID       string `json:"stepId"`
	Status       string `json:"status"`
	Comment      string `json:"comment"`
	Revision     int64  `json:"revision"`
}

type Run struct {
	Variants         []Variant            `json:"variants"`
	Batches          []Batch              `json:"batches"`
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

type VariantVersion struct {
	ID        string              `json:"id"`
	Revision  int64               `json:"revision"`
	Request   catalog.CallRequest `json:"request"`
	CreatedAt time.Time           `json:"createdAt"`
}
type Variant struct {
	ID               string           `json:"id"`
	RunID            string           `json:"runId"`
	BlockID          string           `json:"blockId"`
	Name             string           `json:"name"`
	Position         int              `json:"position"`
	Revision         int64            `json:"revision"`
	CurrentVersionID string           `json:"currentVersionId"`
	Versions         []VariantVersion `json:"versions"`
}
type BatchItem struct {
	Position  int    `json:"position"`
	BlockID   string `json:"blockId"`
	State     string `json:"state"`
	AttemptID string `json:"attemptId,omitempty"`
}
type Batch struct {
	ID         string      `json:"id"`
	RunID      string      `json:"runId"`
	State      string      `json:"state"`
	Items      []BatchItem `json:"items"`
	Completed  int         `json:"completed"`
	Problems   int         `json:"problems"`
	StartedAt  time.Time   `json:"startedAt"`
	FinishedAt *time.Time  `json:"finishedAt,omitempty"`
}
