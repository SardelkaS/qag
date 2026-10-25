package catalog

import "time"

type Folder struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Archived  bool      `json:"archived"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type TestCase struct {
	ID             string    `json:"id"`
	FolderID       string    `json:"folderId"`
	Name           string    `json:"name"`
	Archived       bool      `json:"archived"`
	ParentArchived bool      `json:"parentArchived"`
	Revision       int64     `json:"revision"`
	Steps          []Step    `json:"steps"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type Step struct {
	ID          string  `json:"id"`
	Title       string  `json:"title,omitempty"`
	Description string  `json:"description"`
	Blocks      []Block `json:"blocks"`
}

type KeyValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// TypedValue represents database values without JavaScript number coercion.
// Null has type "null" and no value; all other values have a textual encoding.
type TypedValue struct {
	Type  string `json:"type"`
	Value string `json:"value,omitempty"`
}

type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type Block struct {
	ID             string       `json:"id"`
	Kind           string       `json:"kind"`
	Resource       string       `json:"resource"`
	TimeoutSeconds int          `json:"timeoutSeconds"`
	HTTP           *HTTPRequest `json:"http,omitempty"`
	GRPC           *GRPCRequest `json:"grpc,omitempty"`
	SQL            *SQLRequest  `json:"sql,omitempty"`
	Expected       Expectation  `json:"expected"`
}

type HTTPRequest struct {
	Method   string     `json:"method"`
	Path     string     `json:"path"`
	Query    []KeyValue `json:"query"`
	Headers  []KeyValue `json:"headers"`
	BodyMode string     `json:"bodyMode"`
	Body     string     `json:"body"`
}

type GRPCRequest struct {
	Service  string     `json:"service"`
	Method   string     `json:"method"`
	Body     string     `json:"body"`
	Metadata []KeyValue `json:"metadata"`
}

type SQLRequest struct {
	Query      string       `json:"query"`
	Parameters []TypedValue `json:"parameters"`
	Mode       string       `json:"mode"`
}

type Expectation struct {
	StatusCode   *int           `json:"statusCode,omitempty"`
	GRPCCode     string         `json:"grpcCode,omitempty"`
	Headers      []KeyValue     `json:"headers,omitempty"`
	BodyMode     string         `json:"bodyMode"`
	Body         string         `json:"body,omitempty"`
	Columns      []Column       `json:"columns,omitempty"`
	Rows         [][]TypedValue `json:"rows,omitempty"`
	RowsAffected *string        `json:"rowsAffected,omitempty"`
}
