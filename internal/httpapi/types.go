package httpapi

import (
	"qag/internal/catalog"
	"qag/internal/resources"
	"qag/internal/runs"
)

type TestCaseCreate struct {
	Name string `json:"name,omitempty"`
}
type CreateName struct {
	Name string `json:"name"`
}
type ArchivePatch struct {
	Name     *string `json:"name,omitempty"`
	Archived *bool   `json:"archived,omitempty"`
}
type TestCaseWrite struct {
	Name     string         `json:"name,omitempty"`
	Revision int64          `json:"revision"`
	Steps    []catalog.Step `json:"steps,omitempty"`
}
type EnvironmentWrite struct {
	Description string `json:"description"`
	Name        string `json:"name"`
	Revision    int64  `json:"revision"`
}
type VariablesWrite struct {
	Revision  int64                   `json:"revision"`
	Variables map[string]string       `json:"variables"`
	Secrets   resources.SecretChanges `json:"secrets"`
}
type ResourceWrite struct {
	Name     string                  `json:"name"`
	Kind     string                  `json:"kind"`
	Revision int64                   `json:"revision"`
	Config   resources.Config        `json:"config"`
	Secrets  resources.SecretChanges `json:"secrets"`
}
type ProtoBundle struct {
	Files    map[string]string `json:"files"`
	Revision int64             `json:"revision"`
}
type RunStart struct {
	ExecuteAll    bool   `json:"executeAll,omitempty"`
	EnvironmentID string `json:"environmentId"`
	Revision      int64  `json:"revision"`
}
type VariantWrite struct {
	Name     string         `json:"name"`
	Revision int64          `json:"revision"`
	Request  *catalog.Block `json:"request,omitempty"`
}
type AttemptStart struct {
	EnvironmentID string `json:"environmentId,omitempty"`
	Revision      int64  `json:"revision,omitempty"`
}
type StepWrite struct {
	Status   string `json:"status"`
	Comment  string `json:"comment"`
	Revision int64  `json:"revision"`
}
type FolderPage struct {
	Items      []catalog.Folder `json:"items"`
	NextCursor string           `json:"nextCursor,omitempty"`
}
type TestCasePage struct {
	Items      []catalog.TestCase `json:"items"`
	NextCursor string             `json:"nextCursor,omitempty"`
}
type EnvironmentPage struct {
	Items      []resources.Environment `json:"items"`
	NextCursor string                  `json:"nextCursor,omitempty"`
}
type ResourcePage struct {
	Items      []resources.Resource `json:"items"`
	NextCursor string               `json:"nextCursor,omitempty"`
}
type RunPage struct {
	Items      []runs.Run `json:"items"`
	NextCursor string     `json:"nextCursor,omitempty"`
}
