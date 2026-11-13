// Package dataset defines the complete, versioned, portable state of qag.
// Public API projections are deliberately not used as backup records.
package dataset

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/SardelkaS/qag/internal/catalog"
	"time"

	"gopkg.in/yaml.v3"
)

const Version = 4
const MaxBytes = 256 << 20
const MaxRecords = 100000
const MaxDepth = 64

// JSON keeps historical JSON and decimal tokens without a float conversion.
type JSON string

func Raw(value any) JSON { b, _ := json.Marshal(value); return JSON(b) }
func Value[T any](raw JSON) (T, error) {
	var v T
	err := json.Unmarshal([]byte(raw), &v)
	return v, err
}
func (j JSON) MarshalYAML() (any, error) {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Style: yaml.LiteralStyle, Value: string(j)}, nil
}

type Binary []byte

func (b Binary) MarshalYAML() (any, error) { return base64.StdEncoding.EncodeToString(b), nil }
func (b *Binary) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
		return errors.New("invalid binary")
	}
	v, err := base64.StdEncoding.Strict().DecodeString(n.Value)
	*b = v
	return err
}

type Document struct {
	Format  string `yaml:"format"`
	Version int    `yaml:"version"`
	Data    Data   `yaml:"data"`
}

type Data struct {
	Folders             []Folder             `yaml:"folders"`
	TestCases           []TestCase           `yaml:"testCases"`
	Environments        []Environment        `yaml:"environments"`
	EnvironmentVersions []EnvironmentVersion `yaml:"environmentVersions"`
	Resources           []Resource           `yaml:"resources"`
	ResourceVersions    []ResourceVersion    `yaml:"resourceVersions"`
	Runs                []Run                `yaml:"runs"`
	RunResourceVersions []RunResourceVersion `yaml:"runResourceVersions"`
	RunSteps            []RunStep            `yaml:"runSteps"`
	CallAttempts        []CallAttempt        `yaml:"callAttempts"`
	ExpectedImages      []Image              `yaml:"expectedImages"`
	Variants            []Variant            `yaml:"runCallVariants"`
	VariantVersions     []VariantVersion     `yaml:"variantVersions"`
	Batches             []Batch              `yaml:"runBatches"`
	BatchItems          []BatchItem          `yaml:"batchItems"`
	BuiltinDemo         []Builtin            `yaml:"builtinDemo"`
	DemoItems           []DemoItem           `yaml:"demoItems"`
	HTTPVariableProbes  []HTTPVariableProbe  `yaml:"httpVariableProbes"`
	CallLogs            []CallLog            `yaml:"callLogs"`
}

type HTTPVariableProbe struct {
	ID             string     `yaml:"id"`
	EnvironmentID  string     `yaml:"environmentId"`
	ResourceID     string     `yaml:"resourceId"`
	IdempotencyKey string     `yaml:"idempotencyKey"`
	RequestHash    string     `yaml:"requestHash"`
	State          string     `yaml:"state"`
	StartedAt      time.Time  `yaml:"startedAt"`
	FinishedAt     *time.Time `yaml:"finishedAt"`
	Payload        JSON       `yaml:"payload"`
}

type CallLog struct {
	ID            string    `yaml:"id"`
	AttemptID     string    `yaml:"attemptId"`
	ProbeID       string    `yaml:"probeId"`
	RunID         string    `yaml:"runId"`
	TestCaseID    string    `yaml:"testCaseId"`
	EnvironmentID string    `yaml:"environmentId"`
	ResourceID    string    `yaml:"resourceId"`
	Protocol      string    `yaml:"protocol"`
	Origin        string    `yaml:"origin"`
	Stage         string    `yaml:"stage"`
	State         string    `yaml:"state"`
	StartedAt     time.Time `yaml:"startedAt"`
	Summary       JSON      `yaml:"summary"`
	Payload       JSON      `yaml:"payload"`
}

func Empty() Document { return Document{Format: "qag", Version: Version} }

type Folder struct {
	ID             string     `yaml:"id"`
	Name           string     `yaml:"name"`
	NormalizedName string     `yaml:"normalizedName"`
	ArchivedAt     *time.Time `yaml:"archivedAt"`
	CreatedAt      time.Time  `yaml:"createdAt"`
	UpdatedAt      time.Time  `yaml:"updatedAt"`
}
type TestCase struct {
	ID             string     `yaml:"id"`
	FolderID       string     `yaml:"folderId"`
	Name           string     `yaml:"name"`
	NormalizedName string     `yaml:"normalizedName"`
	ArchivedAt     *time.Time `yaml:"archivedAt"`
	Revision       int64      `yaml:"revision"`
	Steps          JSON       `yaml:"steps"`
	CreatedAt      time.Time  `yaml:"createdAt"`
	UpdatedAt      time.Time  `yaml:"updatedAt"`
}
type Environment struct {
	ID               string    `yaml:"id"`
	Name             string    `yaml:"name"`
	NormalizedName   string    `yaml:"normalizedName"`
	Revision         int64     `yaml:"revision"`
	CurrentVersionID string    `yaml:"currentVersionId"`
	Description      string    `yaml:"description"`
	CreatedAt        time.Time `yaml:"createdAt"`
	UpdatedAt        time.Time `yaml:"updatedAt"`
}
type EnvironmentVersion struct {
	HTTPVariables map[string]catalog.HTTPHeaderSource `yaml:"httpVariables,omitempty"`
	ID            string                              `yaml:"id"`
	EnvironmentID string                              `yaml:"environmentId"`
	Revision      int64                               `yaml:"revision"`
	Variables     map[string]string                   `yaml:"variables"`
	Secrets       map[string]string                   `yaml:"secrets"`
	SecretNames   []string                            `yaml:"secretNames"`
	CreatedAt     time.Time                           `yaml:"createdAt"`
}
type Resource struct {
	ID               string    `yaml:"id"`
	EnvironmentID    string    `yaml:"environmentId"`
	Name             string    `yaml:"name"`
	Kind             string    `yaml:"kind"`
	Revision         int64     `yaml:"revision"`
	CurrentVersionID string    `yaml:"currentVersionId"`
	CreatedAt        time.Time `yaml:"createdAt"`
	UpdatedAt        time.Time `yaml:"updatedAt"`
}
type ResourceVersion struct {
	ID          string            `yaml:"id"`
	ResourceID  string            `yaml:"resourceId"`
	Revision    int64             `yaml:"revision"`
	Config      JSON              `yaml:"config"`
	Secrets     map[string]string `yaml:"secrets"`
	SecretNames []string          `yaml:"secretNames"`
	CreatedAt   time.Time         `yaml:"createdAt"`
}
type Run struct {
	HistoryPolicy        string     `yaml:"historyPolicy,omitempty"`
	ID                   string     `yaml:"id"`
	TestCaseID           string     `yaml:"testCaseId"`
	EnvironmentVersionID string     `yaml:"environmentVersionId"`
	Snapshot             JSON       `yaml:"snapshot"`
	Context              JSON       `yaml:"context"`
	Status               string     `yaml:"status"`
	RetentionReasons     []string   `yaml:"retentionReasons"`
	IdempotencyKey       string     `yaml:"idempotencyKey"`
	RequestHash          string     `yaml:"requestHash"`
	StartedAt            time.Time  `yaml:"startedAt"`
	FinishedAt           *time.Time `yaml:"finishedAt"`
}
type RunResourceVersion struct {
	RunID             string `yaml:"runId"`
	ResourceName      string `yaml:"resourceName"`
	ResourceVersionID string `yaml:"resourceVersionId"`
}
type RunStep struct {
	RunID    string `yaml:"runId"`
	StepID   string `yaml:"stepId"`
	Status   string `yaml:"status"`
	Comment  string `yaml:"comment"`
	Revision int64  `yaml:"revision"`
}
type CallAttempt struct {
	ID                   string     `yaml:"id"`
	RunID                string     `yaml:"runId"`
	TestCaseID           string     `yaml:"testCaseId"`
	BlockID              string     `yaml:"blockId"`
	CaseRevision         int64      `yaml:"caseRevision"`
	EnvironmentVersionID string     `yaml:"environmentVersionId"`
	ResourceVersionID    string     `yaml:"resourceVersionId"`
	AttemptNumber        int        `yaml:"attemptNumber"`
	IdempotencyKey       string     `yaml:"idempotencyKey"`
	RequestHash          string     `yaml:"requestHash"`
	State                string     `yaml:"state"`
	SafeRequest          JSON       `yaml:"safeRequest"`
	Result               JSON       `yaml:"result"`
	StartedAt            time.Time  `yaml:"startedAt"`
	FinishedAt           *time.Time `yaml:"finishedAt"`
	DurationMS           int64      `yaml:"durationMs"`
	VariantID            string     `yaml:"variantId"`
	VariantVersionID     string     `yaml:"variantVersionId"`
	BatchID              string     `yaml:"batchId"`
	BatchPosition        int        `yaml:"batchPosition"`
}
type Image struct {
	ID          string    `yaml:"id"`
	TestCaseID  string    `yaml:"testCaseId"`
	Filename    string    `yaml:"filename"`
	ContentType string    `yaml:"contentType"`
	SizeBytes   int       `yaml:"sizeBytes"`
	Width       int       `yaml:"width"`
	Height      int       `yaml:"height"`
	SHA256      string    `yaml:"sha256"`
	Data        Binary    `yaml:"data"`
	CreatedAt   time.Time `yaml:"createdAt"`
}
type Variant struct {
	ID               string `yaml:"id"`
	RunID            string `yaml:"runId"`
	BlockID          string `yaml:"blockId"`
	Name             string `yaml:"name"`
	Position         int    `yaml:"position"`
	Revision         int64  `yaml:"revision"`
	CurrentVersionID string `yaml:"currentVersionId"`
}
type VariantVersion struct {
	ID        string    `yaml:"id"`
	VariantID string    `yaml:"variantId"`
	RunID     string    `yaml:"runId"`
	BlockID   string    `yaml:"blockId"`
	Revision  int64     `yaml:"revision"`
	Request   JSON      `yaml:"request"`
	CreatedAt time.Time `yaml:"createdAt"`
}
type Batch struct {
	StepID         string     `yaml:"stepId,omitempty"`
	ID             string     `yaml:"id"`
	RunID          string     `yaml:"runId"`
	IdempotencyKey string     `yaml:"idempotencyKey"`
	RequestHash    string     `yaml:"requestHash"`
	State          string     `yaml:"state"`
	StartedAt      time.Time  `yaml:"startedAt"`
	FinishedAt     *time.Time `yaml:"finishedAt"`
}
type BatchItem struct {
	BatchID   string `yaml:"batchId"`
	RunID     string `yaml:"runId"`
	Position  int    `yaml:"position"`
	BlockID   string `yaml:"blockId"`
	State     string `yaml:"state"`
	AttemptID string `yaml:"attemptId"`
}
type Builtin struct {
	Key            string `yaml:"key"`
	FolderID       string `yaml:"folderId"`
	TestCaseID     string `yaml:"testCaseId"`
	EnvironmentID  string `yaml:"environmentId"`
	HTTPResourceID string `yaml:"httpResourceId"`
	GRPCResourceID string `yaml:"grpcResourceId"`
	SQLResourceID  string `yaml:"sqlResourceId"`
	SeedVersion    int    `yaml:"seedVersion"`
}
type DemoItem struct {
	ID     string `yaml:"id"`
	Name   string `yaml:"name"`
	Status string `yaml:"status"`
	Amount string `yaml:"amount"`
}
