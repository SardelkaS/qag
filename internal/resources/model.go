package resources

import (
	"qag/internal/catalog"
	"time"
)

type Environment struct {
	Description string            `json:"description"`
	ReadOnly    bool              `json:"readOnly"`
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Revision    int64             `json:"revision"`
	Variables   map[string]string `json:"variables"`
	SecretNames []string          `json:"secretNames"`
}

type TLSConfig = catalog.TLSConfig

type Config struct {
	BaseURL    string                  `json:"baseUrl,omitempty"`
	Address    string                  `json:"address,omitempty"`
	Database   string                  `json:"database,omitempty"`
	Username   string                  `json:"username,omitempty"`
	Headers    []catalog.RequestHeader `json:"headers"`
	TLS        TLSConfig               `json:"tls"`
	ProtoFiles map[string]string       `json:"protoFiles,omitempty"`
}

// Resource is the public representation. Secret values are never returned.
type Resource struct {
	ReadOnly      bool      `json:"readOnly"`
	ID            string    `json:"id"`
	EnvironmentID string    `json:"environmentId"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	VersionID     string    `json:"versionId"`
	Revision      int64     `json:"revision"`
	Config        Config    `json:"config"`
	SecretNames   []string  `json:"secretNames"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// SecretChanges distinguishes replacing a secret, including with an empty
// value, from retaining or explicitly clearing a previously stored secret.
type SecretChanges struct {
	Replace map[string]string `json:"replace"`
	Clear   []string          `json:"clear"`
}
