package runs

import (
	"qag/internal/catalog"
	"qag/internal/execution"
	"time"
)

// Report contains only public, persisted fields. Resource credentials and TLS
// material are deliberately absent from this wire model.
type Report struct {
	OwnerCaseID     string        `json:"-"`
	ClipboardNative bool          `json:"clipboardNative"`
	Document        []ReportNode  `json:"document"`
	SnapshotID      string        `json:"snapshotId"`
	Hash            string        `json:"hash"`
	RunID           string        `json:"runId"`
	Title           string        `json:"title"`
	EnvironmentName string        `json:"environmentName"`
	Status          string        `json:"status"`
	Partial         bool          `json:"partial"`
	IncludeExpected bool          `json:"includeExpected"`
	CreatedAt       time.Time     `json:"createdAt"`
	StartedAt       time.Time     `json:"startedAt"`
	FinishedAt      *time.Time    `json:"finishedAt,omitempty"`
	Steps           []ReportStep  `json:"steps"`
	Batches         []Batch       `json:"batches"`
	Images          []ReportImage `json:"images"`
}
type ReportNode struct {
	ID      string                 `json:"id"`
	Kind    string                 `json:"kind"`
	Text    string                 `json:"text"`
	Level   int                    `json:"level,omitempty"`
	Status  string                 `json:"status,omitempty"`
	CallID  string                 `json:"callId,omitempty"`
	ImageID string                 `json:"imageId,omitempty"`
	Columns []catalog.Column       `json:"columns,omitempty"`
	Rows    [][]catalog.TypedValue `json:"rows,omitempty"`
}
type ReportStep struct {
	ID           string                   `json:"id"`
	Title        string                   `json:"title"`
	Description  string                   `json:"description"`
	Status       string                   `json:"status"`
	LegacyStatus string                   `json:"legacyStatus,omitempty"`
	Comment      string                   `json:"comment"`
	Expected     *catalog.StepExpectation `json:"expected,omitempty"`
	Calls        []ReportCall             `json:"calls"`
}
type ReportRequest struct {
	TimeoutSeconds int                  `json:"timeoutSeconds"`
	HTTP           *catalog.HTTPRequest `json:"http,omitempty"`
	GRPC           *catalog.GRPCRequest `json:"grpc,omitempty"`
	SQL            *catalog.SQLRequest  `json:"sql,omitempty"`
}
type ReportCall struct {
	ID       string               `json:"id"`
	Kind     string               `json:"kind"`
	Resource string               `json:"resource"`
	Request  ReportRequest        `json:"request"`
	Expected *catalog.Expectation `json:"expected,omitempty"`
	Attempts []ReportAttempt      `json:"attempts"`
	Variants []ReportVariant      `json:"variants"`
}
type ReportVariant struct {
	ID       string                 `json:"id"`
	Name     string                 `json:"name"`
	Position int                    `json:"position"`
	Revision int64                  `json:"revision"`
	Versions []ReportVariantVersion `json:"versions"`
	Attempts []ReportAttempt        `json:"attempts"`
}
type ReportVariantVersion struct {
	ID        string        `json:"id"`
	Revision  int64         `json:"revision"`
	Request   ReportRequest `json:"request"`
	CreatedAt time.Time     `json:"createdAt"`
}
type ReportAttempt struct {
	ID               string               `json:"id"`
	Number           int                  `json:"number"`
	VariantVersionID string               `json:"variantVersionId,omitempty"`
	State            string               `json:"state"`
	StartedAt        time.Time            `json:"startedAt"`
	FinishedAt       *time.Time           `json:"finishedAt,omitempty"`
	DurationMS       int64                `json:"durationMs"`
	Request          ReportRequest        `json:"request"`
	Expected         *catalog.Expectation `json:"expected,omitempty"`
	Result           *execution.Result    `json:"result,omitempty"`
}
type ReportImage struct {
	ID     string `json:"id"`
	MIME   string `json:"mime"`
	Data   string `json:"data"`
	SHA256 string `json:"sha256"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}
