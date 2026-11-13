package execution

import (
	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/comparison"
	"github.com/SardelkaS/qag/internal/resources"
	"time"
)

type Result struct {
	VariableSources    []VariableSourceResult `json:"variableSources,omitempty"`
	HeaderSources      []HeaderSourceResult   `json:"headerSources,omitempty"`
	MainRequestStarted *bool                  `json:"mainRequestStarted,omitempty"`
	Protocol           string                 `json:"protocol"`
	StatusCode         *int                   `json:"statusCode,omitempty"`
	GRPCCode           string                 `json:"grpcCode,omitempty"`
	StatusMessage      string                 `json:"statusMessage,omitempty"`
	Headers            []catalog.KeyValue     `json:"headers"`
	Trailers           []catalog.KeyValue     `json:"trailers,omitempty"`
	Body               string                 `json:"body"`
	Columns            []catalog.Column       `json:"columns,omitempty"`
	Rows               [][]catalog.TypedValue `json:"rows,omitempty"`
	RowsAffected       *string                `json:"rowsAffected,omitempty"`
	ErrorCode          string                 `json:"errorCode,omitempty"`
	Error              string                 `json:"error,omitempty"`
	Truncated          bool                   `json:"truncated"`
	Redacted           bool                   `json:"redacted"`
	Comparison         comparison.Result      `json:"comparison"`
}

type Attempt struct {
	VariantID          string              `json:"variantId,omitempty"`
	VariantVersionID   string              `json:"variantVersionId,omitempty"`
	BatchID            string              `json:"batchId,omitempty"`
	BatchPosition      int                 `json:"batchPosition,omitempty"`
	ID                 string              `json:"id"`
	RunID              string              `json:"runId,omitempty"`
	TestCaseID         string              `json:"testCaseId"`
	BlockID            string              `json:"blockId"`
	Revision           int64               `json:"revision"`
	EnvironmentID      string              `json:"environmentId"`
	EnvironmentName    string              `json:"environmentName"`
	Resource           resources.Resource  `json:"resource"`
	Request            catalog.CallRequest `json:"request"`
	Number             int                 `json:"number"`
	State              string              `json:"state"`
	StartedAt          time.Time           `json:"startedAt"`
	FinishedAt         *time.Time          `json:"finishedAt,omitempty"`
	DurationMS         int64               `json:"durationMs"`
	PersistencePending bool                `json:"persistencePending"`
	Result             *Result             `json:"result,omitempty"`
}
