// Package ports defines the storage contracts shared by native backends.
package ports

import (
	"context"
	"github.com/SardelkaS/qag/internal/builtin"
	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/execution"
	"github.com/SardelkaS/qag/internal/resources"
	"github.com/SardelkaS/qag/internal/runs"
	"time"
)

type Catalog interface {
	UploadImage(ctx context.Context, id string, upload catalog.ImageUpload) (catalog.ExpectedImage, error)
	ReadImage(ctx context.Context, id, imageID string) (catalog.ExpectedImage, []byte, error)
	CheckImages(ctx context.Context, id string, steps []catalog.Step) error
	ListFolders(ctx context.Context, archived bool, limit int, cursor string) (catalog.Page[catalog.Folder], error)
	CreateFolder(ctx context.Context, value string) (catalog.Folder, error)
	PatchFolder(ctx context.Context, id string, newName *string, archived *bool) (catalog.Folder, error)
	GetCase(ctx context.Context, id string) (catalog.TestCase, error)
	DemoInfo(ctx context.Context) (builtin.Info, error)
	CheckEnvironment(ctx context.Context, id, env string) error
	ListCases(ctx context.Context, folderID string, limit int, cursor string) (catalog.Page[catalog.TestCase], error)
	CreateCase(ctx context.Context, folderID, value string) (catalog.TestCase, error)
	SaveCase(ctx context.Context, id, value string, revision int64, steps []catalog.Step) (catalog.TestCase, error)
	PatchCase(ctx context.Context, id string, newName *string, archived *bool) (catalog.TestCase, error)
}

var _ Catalog = (*catalog.Repository)(nil)

type Resources interface {
	GetEnvironment(ctx context.Context, id string) (resources.Environment, error)
	ListEnvironments(ctx context.Context, limit int, cursor string) (catalog.Page[resources.Environment], error)
	SaveEnvironment(ctx context.Context, id, value string, revision int64, variables map[string]string, changes resources.SecretChanges) (resources.Environment, error)
	SaveEnvironmentMetadata(ctx context.Context, id, name, description string, revision int64) (resources.Environment, error)
	SaveVariables(ctx context.Context, id string, revision int64, variables map[string]string, changes resources.SecretChanges, sources ...map[string]catalog.HTTPHeaderSource) (resources.Environment, error)
	GetResource(ctx context.Context, id string) (resources.Resource, error)
	ListResources(ctx context.Context, environmentID string, limit int, cursor string) (catalog.Page[resources.Resource], error)
	SaveResource(ctx context.Context, id, environmentID, name, kind string, revision int64, config resources.Config, changes resources.SecretChanges) (resources.Resource, error)
	UploadProto(ctx context.Context, id string, revision int64, files map[string]string) (resources.Resource, error)
	Freeze(ctx context.Context, environmentID string, needs map[string]string) (resources.Snapshot, error)
	Resolve(ctx context.Context, snapshot resources.Snapshot, name string) (resources.Resolved, error)
}

var _ Resources = (*resources.Repository)(nil)

type Runs interface {
	GetAttempt(ctx context.Context, id string) (execution.Attempt, error)
	LookupAttempt(ctx context.Context, key, hash string) (execution.Attempt, bool, error)
	BeginAttempt(ctx context.Context, candidateID, caseID, runID string, revision int64, block catalog.Block, snapshot resources.Snapshot, safeBlock catalog.Block, key, hash string, options ...runs.AttemptOptions) (execution.Attempt, bool, error)
	ClaimAttempt(ctx context.Context, id string) (bool, error)
	CheckAttemptRunnable(ctx context.Context, id string) error
	CompleteAttempt(ctx context.Context, id, state string, result execution.Result, duration time.Duration) error
	CancelAttempt(ctx context.Context, id string) (execution.Attempt, error)
	StartBatch(ctx context.Context, runID, key, hash string, stepID ...string) (runs.Batch, error)
	AdvanceBatch(ctx context.Context, runID, batchID string) error
	PassAll(ctx context.Context, id string, steps []runs.StepRevision) (runs.Run, error)
	ReportSnapshot(ctx context.Context, id string, includeExpected bool) (runs.Report, error)
	LookupRun(ctx context.Context, key, hash string) (runs.Run, bool, error)
	Start(ctx context.Context, snapshot catalog.TestCase, environment resources.Snapshot, key, hash string, executeAll ...bool) (runs.Run, error)
	StartWithOptions(ctx context.Context, snapshot catalog.TestCase, environment resources.Snapshot, key, hash string, options runs.StartOptions) (runs.Run, error)
	Get(ctx context.Context, id string) (runs.Run, error)
	Context(ctx context.Context, id string) (resources.Snapshot, error)
	List(ctx context.Context, caseID string, retained *bool, limit int, cursor string, active ...bool) (catalog.Page[runs.Run], error)
	UpdateStep(ctx context.Context, id, stepID, status, comment string, revision int64) (runs.Run, error)
	Finish(ctx context.Context, id string) (runs.Run, error)
	Cancel(ctx context.Context, id string) (runs.Run, error)
	Save(ctx context.Context, id string) (runs.Run, error)
	Delete(ctx context.Context, id string) error
	Recover(ctx context.Context) error
	SaveVariant(ctx context.Context, runID, blockID, id, name string, revision int64, request *catalog.CallRequest) (runs.Variant, error)
}

var _ Runs = (*runs.Repository)(nil)

type Lifecycle interface {
	Ping(context.Context) error
	Close()
	InstanceLost() <-chan struct{}
}
