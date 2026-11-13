package mcpserver

import (
	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/reporting"
	"github.com/SardelkaS/qag/internal/resources"
	"github.com/SardelkaS/qag/internal/runs"
)

type Empty struct{}
type Page struct {
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}
type IDInput struct {
	ID string `json:"id"`
}
type FolderList struct {
	Page
	Archived bool `json:"archived,omitempty"`
}
type NameInput struct {
	Name string `json:"name"`
}
type Update struct {
	IDInput
	Name     *string `json:"name,omitempty"`
	Archived *bool   `json:"archived,omitempty"`
}
type CasesList struct {
	Page
	FolderID string `json:"folderId"`
}
type CaseCreate struct {
	FolderID string `json:"folderId"`
	Name     string `json:"name,omitempty"`
}
type CaseSave struct {
	IDInput
	Revision int64          `json:"revision"`
	Name     string         `json:"name,omitempty"`
	Steps    []catalog.Step `json:"steps,omitempty"`
}
type ImageUpload struct {
	TestCaseID string `json:"testCaseId"`
	catalog.ImageUpload
}
type ImageGet struct {
	TestCaseID string `json:"testCaseId"`
	ImageID    string `json:"imageId"`
}
type ImageResult struct {
	Image    catalog.ExpectedImage `json:"image"`
	Artifact Artifact              `json:"artifact"`
}
type EnvironmentCreate struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}
type EnvironmentUpdate struct {
	IDInput
	EnvironmentCreate
	Revision int64 `json:"revision"`
}
type VariablesSet struct {
	IDInput
	Revision  int64             `json:"revision"`
	Variables map[string]string `json:"variables"`
	Secrets   SecretChanges     `json:"secrets,omitempty"`
}
type SecretChanges struct {
	Replace map[string]string `json:"replace,omitempty"`
	Clear   []string          `json:"clear,omitempty"`
}

func (s SecretChanges) domain() resources.SecretChanges {
	return resources.SecretChanges{Replace: s.Replace, Clear: s.Clear}
}

type ResourceList struct {
	Page
	EnvironmentID string `json:"environmentId"`
}
type ResourceData struct {
	Name    string           `json:"name"`
	Kind    string           `json:"kind"`
	Config  resources.Config `json:"config"`
	Secrets SecretChanges    `json:"secrets,omitempty"`
}
type ResourceCreate struct {
	EnvironmentID string `json:"environmentId"`
	ResourceData
}
type ResourceUpdate struct {
	IDInput
	Revision int64 `json:"revision"`
	ResourceData
}
type ProtoUpload struct {
	IDInput
	Revision int64             `json:"revision"`
	Files    map[string]string `json:"files"`
}
type RunsList struct {
	Page
	TestCaseID string `json:"testCaseId"`
	Retained   *bool  `json:"retained,omitempty"`
	Active     bool   `json:"active,omitempty"`
}
type RunStart struct {
	HistoryPolicy  string `json:"historyPolicy,omitempty"`
	TestCaseID     string `json:"testCaseId"`
	EnvironmentID  string `json:"environmentId"`
	Revision       int64  `json:"revision"`
	ExecuteAll     bool   `json:"executeAll,omitempty"`
	IdempotencyKey string `json:"idempotencyKey"`
}
type RunStepUpdate struct {
	RunID    string `json:"runId"`
	StepID   string `json:"stepId"`
	Revision int64  `json:"revision"`
	Status   string `json:"status"`
	Comment  string `json:"comment"`
}
type RunPassAll struct {
	RunID string              `json:"runId"`
	Steps []runs.StepRevision `json:"steps"`
}
type BatchStart struct {
	RunID          string `json:"runId"`
	IdempotencyKey string `json:"idempotencyKey"`
}

type StepExecute struct {
	RunID          string `json:"runId"`
	StepID         string `json:"stepId"`
	IdempotencyKey string `json:"idempotencyKey"`
}

type CaseExportInput struct {
	ID              string `json:"id"`
	Revision        int64  `json:"revision"`
	Format          string `json:"format"`
	IncludeExpected *bool  `json:"includeExpected,omitempty"`
}
type RunAttemptStart struct {
	RunID          string `json:"runId"`
	BlockID        string `json:"blockId"`
	IdempotencyKey string `json:"idempotencyKey"`
}
type EditorAttemptStart struct {
	TestCaseID     string `json:"testCaseId"`
	BlockID        string `json:"blockId"`
	EnvironmentID  string `json:"environmentId"`
	Revision       int64  `json:"revision"`
	IdempotencyKey string `json:"idempotencyKey"`
}
type VariantCreate struct {
	RunID   string `json:"runId"`
	BlockID string `json:"blockId"`
	Name    string `json:"name,omitempty"`
}
type ResearchRequest struct {
	TimeoutSeconds int                  `json:"timeoutSeconds,omitempty"`
	HTTP           *catalog.HTTPRequest `json:"http,omitempty"`
	GRPC           *catalog.GRPCRequest `json:"grpc,omitempty"`
	SQL            *catalog.SQLRequest  `json:"sql,omitempty"`
}
type VariantUpdate struct {
	RunID     string          `json:"runId"`
	VariantID string          `json:"variantId"`
	Name      string          `json:"name,omitempty"`
	Revision  int64           `json:"revision"`
	Request   ResearchRequest `json:"request"`
}
type VariantAttemptStart struct {
	RunID          string `json:"runId"`
	VariantID      string `json:"variantId"`
	Revision       int64  `json:"revision"`
	IdempotencyKey string `json:"idempotencyKey"`
}
type ReportPrepare struct {
	RunID           string `json:"runId"`
	IncludeExpected *bool  `json:"includeExpected,omitempty"`
}
type ReportExport struct {
	reporting.Reference
	Format string `json:"format"`
}
type ArtifactRead struct {
	IDInput
	Offset int64 `json:"offset,omitempty"`
	Length int   `json:"length,omitempty"`
}
type ExportResult struct {
	Files []Artifact `json:"files"`
}
type CopyResult struct {
	Copied  bool       `json:"copied"`
	Code    string     `json:"code,omitempty"`
	Message string     `json:"message,omitempty"`
	Files   []Artifact `json:"files"`
}
type Deleted struct {
	Deleted bool `json:"deleted"`
}
type Status struct {
	Status          string           `json:"status"`
	Protocols       []string         `json:"protocols"`
	ClipboardNative bool             `json:"clipboardNative"`
	Limits          map[string]int64 `json:"limits"`
}
