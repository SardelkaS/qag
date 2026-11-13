package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/problem"
	"github.com/SardelkaS/qag/internal/reporting"
	"github.com/SardelkaS/qag/internal/resources"
	"github.com/SardelkaS/qag/internal/service"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Options struct {
	Engine  *service.Engine
	Reports *reporting.Service
	Ping    func(context.Context) error
	Logger  *slog.Logger
}
type Server struct {
	SDK        *mcp.Server
	Artifacts  *ArtifactStore
	options    Options
	tools      []*mcp.Tool
	resourceMu sync.Mutex
	registered map[string]bool
}
type ToolError struct {
	Code        string            `json:"code"`
	Message     string            `json:"message"`
	FieldErrors map[string]string `json:"fieldErrors,omitempty"`
	RequestID   string            `json:"requestId"`
}
type Summary struct {
	ID         string `json:"id,omitempty"`
	State      string `json:"state,omitempty"`
	Status     string `json:"status,omitempty"`
	NextCursor string `json:"nextCursor,omitempty"`
}
type Envelope[T any] struct {
	Data     *T         `json:"data,omitempty"`
	Artifact *Artifact  `json:"artifact,omitempty"`
	Summary  *Summary   `json:"summary,omitempty"`
	Error    *ToolError `json:"error,omitempty"`
}

func New(options Options) *Server {
	server := &Server{options: options, Artifacts: NewArtifactStore(), registered: map[string]bool{}}
	server.SDK = mcp.NewServer(&mcp.Implementation{Name: "qag", Version: "1.0.0"}, &mcp.ServerOptions{Instructions: serverInstructions, SupportedProtocolVersions: []string{"2026-07-28", "2025-11-25"}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}, Resources: &mcp.ResourceCapabilities{}}, SetCacheable: func(_ context.Context, _ mcp.Request, c *mcp.Cacheable) { c.TTLMs = 0; c.CacheScope = "private" }})
	server.SDK.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "resources/list" || method == "resources/read" {
				server.syncResources()
			}
			return next(ctx, method, req)
		}
	})
	server.registerTools()
	server.registerGuide()
	return server
}
func (s *Server) Handler() http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.SDK }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 32 << 20, PropagateRequestCancellation: true, DisableLocalhostProtection: true})
}
func (s *Server) Manifest() []*mcp.Tool {
	out := append([]*mcp.Tool{}, s.tools...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (s *Server) syncResources() {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	current := map[string]bool{}
	for _, a := range s.Artifacts.List() {
		current[a.URI] = true
		if s.registered[a.URI] {
			continue
		}
		artifact := a
		s.SDK.AddResource(&mcp.Resource{URI: a.URI, Name: a.Name, MIMEType: a.MIMEType, Size: a.Size}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			_, data, err := s.Artifacts.Read(artifact.ID, 0, 0, true)
			if err != nil {
				return nil, fmtResourceError(err)
			}
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: artifact.URI, MIMEType: artifact.MIMEType, Blob: data}}}, nil
		})
	}
	for uri := range s.registered {
		if !current[uri] {
			s.SDK.RemoveResources(uri)
		}
	}
	s.registered = current
}
func fmtResourceError(err error) error {
	var p *problem.Error
	if errors.As(err, &p) {
		return &jsonrpc.Error{Code: -32602, Message: p.Message}
	}
	return &jsonrpc.Error{Code: -32603, Message: "Не удалось прочитать артефакт"}
}
func schemaFor[T any]() *jsonschema.Schema {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		panic(err)
	}
	return schema
}
func addTool[I, O any](s *Server, name, description string, readOnly, external, idempotent bool, apply func(context.Context, I) (O, error)) {
	input := schemaFor[I]()
	configureInput(input, name)
	output := schemaFor[Envelope[O]]()
	configureSourceSchemas(output)
	if _, err := output.Resolve(nil); err != nil {
		panic(err)
	}
	resolved, err := input.Resolve(nil)
	if err != nil {
		panic(err)
	}
	destructive := !readOnly && !strings.HasSuffix(name, "_create")
	tool := &mcp.Tool{Name: name, Description: description, InputSchema: input, OutputSchema: output, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly, IdempotentHint: readOnly || idempotent, OpenWorldHint: &external, DestructiveHint: &destructive}}
	s.tools = append(s.tools, tool)
	s.SDK.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		raw := req.Params.Arguments
		if len(raw) == 0 {
			raw = json.RawMessage(`{}`)
		}
		if name == "qag_variables_set" || name == "qag_resource_create" || name == "qag_resource_update" {
			raw = resources.NormalizeVariableWriteJSON(raw)
		}
		// Validate shape separately; normalization preserves authored strings and
		// numeric tokens for the typed decoder.
		var shape any
		if json.Unmarshal(raw, &shape) != nil || resolved.Validate(shape) != nil {
			return nil, &jsonrpc.Error{Code: -32602, Message: "Некорректные параметры инструмента; проверьте его JSON-схему"}
		}
		var input I
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil {
			return nil, &jsonrpc.Error{Code: -32602, Message: "Некорректные поля инструмента"}
		}
		duration := 10 * time.Second
		if name == "qag_report_export" || name == "qag_report_copy" {
			duration = 60 * time.Second
		}
		ctx, cancel := context.WithTimeout(ctx, duration)
		defer cancel()
		id := catalog.NewID()
		started := time.Now()
		value, actionErr := apply(ctx, input)
		out := Envelope[O]{}
		if actionErr != nil {
			out.Error = &ToolError{Code: "internal_error", Message: "Не удалось выполнить действие", RequestID: id}
			var issue *problem.Error
			if errors.As(actionErr, &issue) {
				out.Error.Code = issue.Code
				out.Error.Message = issue.Message
				out.Error.FieldErrors = issue.Fields
			} else if errors.Is(actionErr, context.Canceled) || errors.Is(actionErr, context.DeadlineExceeded) {
				out.Error.Code = "cancelled"
				out.Error.Message = "Ожидание отменено или истекло; проверьте сохранённое состояние перед повтором"
			}
		}
		if actionErr == nil {
			data, e := json.Marshal(value)
			if e != nil {
				return nil, &jsonrpc.Error{Code: -32603, Message: "Не удалось подготовить ответ"}
			}
			if len(data) > InlineBytes {
				artifacts, e := s.Artifacts.PutGroup([]File{{Name: name + ".json", MIMEType: "application/json", Data: data}})
				if e != nil {
					out.Error = &ToolError{Code: "result_too_large", Message: "Результат превышает допустимый предел артефакта", RequestID: id}
				} else {
					out.Artifact = &artifacts[0]
					out.Summary = summarize(value)
				}
			} else {
				out.Data = &value
			}
		}
		s.syncResources()
		encoded, e := json.Marshal(out)
		if e != nil {
			return nil, &jsonrpc.Error{Code: -32603, Message: "Не удалось подготовить ответ"}
		}
		if s.options.Logger != nil {
			code := "ok"
			if out.Error != nil {
				code = out.Error.Code
			}
			s.options.Logger.Info("mcp tool", "tool", name, "requestId", id, "code", code, "durationMs", time.Since(started).Milliseconds())
		}
		result := &mcp.CallToolResult{StructuredContent: out, Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}, IsError: out.Error != nil}
		if out.Artifact != nil {
			result.Content = append(result.Content, &mcp.ResourceLink{URI: out.Artifact.URI, Name: out.Artifact.Name, MIMEType: out.Artifact.MIMEType})
		}
		return result, nil
	})
}

func configureInput(schema *jsonschema.Schema, name string) {
	configureSourceSchemas(schema)
	allowUnnamed := func(values *jsonschema.Schema) {
		if values != nil {
			values.PatternProperties = map[string]*jsonschema.Schema{`^\s*$`: {}}
		}
	}
	if name == "qag_variables_set" {
		allowUnnamed(schema.Properties["variables"])
		allowUnnamed(schema.Properties["httpVariables"])
	}
	if secrets := schema.Properties["secrets"]; secrets != nil {
		allowUnnamed(secrets.Properties["replace"])
	}
	if config := schema.Properties["config"]; config != nil {
		if variables := config.Properties["variables"]; variables != nil && variables.AdditionalProperties != nil {
			allowUnnamed(variables)
			if mode := variables.AdditionalProperties.Properties["mode"]; mode != nil {
				mode.Enum = []any{"literal", "http"}
			}
		}
	}
	for key, value := range schema.Properties {
		if key == "id" || strings.HasSuffix(key, "Id") || key == "idempotencyKey" {
			value.Pattern = `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`
		}
		if key == "revision" {
			min := 1.
			value.Minimum = &min
		}
		if key == "limit" {
			min, max := 1., 100.
			value.Minimum = &min
			value.Maximum = &max
		}
		if key == "status" {
			value.Enum = []any{"not_run", "passed", "failed"}
		}
		if key == "historyPolicy" {
			value.Enum = []any{"auto", "retain", "unretained"}
		}
		if key == "format" {
			value.Enum = []any{"text", "markdown", "rich"}
			if name == "qag_test_case_export" {
				value.Enum = []any{"jira-wiki"}
			}
			if name == "qag_report_export" {
				value.Enum = append(value.Enum, "pdf")
			}
		}
	}
}

func configureSourceSchemas(root *jsonschema.Schema) {
	seen := map[*jsonschema.Schema]bool{}
	var visit func(*jsonschema.Schema)
	visit = func(schema *jsonschema.Schema) {
		if schema == nil || seen[schema] {
			return
		}
		seen[schema] = true
		if schema.Properties["jsonPointer"] != nil && schema.Properties["fetchMode"] != nil && schema.Properties["url"] != nil {
			schema.Properties["bodyMode"].Enum = []any{"none", "text", "json", "form"}
			schema.Properties["fetchMode"].Enum = []any{"per_attempt", "per_run"}
		}
		for _, child := range schema.Properties {
			visit(child)
		}
		for _, child := range schema.Defs {
			visit(child)
		}
		visit(schema.AdditionalProperties)
		visit(schema.Items)
	}
	visit(root)
}
func summarize(value any) *Summary {
	v := reflect.ValueOf(value)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	out := &Summary{}
	if v.Kind() == reflect.Struct {
		for _, field := range []struct {
			name   string
			target *string
		}{{"ID", &out.ID}, {"State", &out.State}, {"Status", &out.Status}, {"NextCursor", &out.NextCursor}} {
			f := v.FieldByName(field.name)
			if f.IsValid() && f.Kind() == reflect.String {
				*field.target = f.String()
			}
		}
	}
	return out
}
