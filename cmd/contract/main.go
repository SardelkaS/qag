// Command contract generates the OpenAPI schemas from the Go wire models.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"time"

	"qag/internal/builtin"
	"qag/internal/catalog"
	"qag/internal/comparison"
	"qag/internal/execution"
	"qag/internal/httpapi"
	"qag/internal/resources"
	"qag/internal/runs"
)

type object = map[string]any

var names = map[reflect.Type]string{}
var schemas = object{}

func register(name string, value any) { names[reflect.TypeOf(value)] = name }

func schema(t reflect.Type) object {
	if t.Kind() == reflect.Pointer {
		return schema(t.Elem())
	}
	if t == reflect.TypeOf(time.Time{}) {
		return object{"type": "string", "format": "date-time"}
	}
	switch t.Kind() {
	case reflect.String:
		return object{"type": "string"}
	case reflect.Bool:
		return object{"type": "boolean"}
	case reflect.Int, reflect.Int32, reflect.Int64:
		return object{"type": "integer", "format": "int64"}
	case reflect.Slice:
		return object{"type": "array", "items": schema(t.Elem())}
	case reflect.Map:
		return object{"type": "object", "additionalProperties": schema(t.Elem())}
	case reflect.Struct:
		name := names[t]
		if name == "" {
			name = t.Name()
			names[t] = name
		}
		if _, ok := schemas[name]; !ok {
			schemas[name] = object{}
			properties := object{}
			required := []string{}
			for i := 0; i < t.NumField(); i++ {
				field := t.Field(i)
				tag := field.Tag.Get("json")
				parts := strings.Split(tag, ",")
				if parts[0] == "" || parts[0] == "-" {
					continue
				}
				properties[parts[0]] = schema(field.Type)
				if !strings.Contains(tag, ",omitempty") {
					required = append(required, parts[0])
				}
			}
			schemas[name] = object{"type": "object", "additionalProperties": false, "properties": properties, "required": required}
		}
		return ref(name)
	default:
		panic("unsupported wire type: " + t.String())
	}
}

func ref(name string) object { return object{"$ref": "#/components/schemas/" + name} }
func response(name string) object {
	return object{"description": "Успешный ответ", "content": object{"application/json": object{"schema": ref(name)}}}
}
func request(name string) object {
	return object{"required": true, "content": object{"application/json": object{"schema": ref(name)}}}
}

func main() {
	for name, value := range map[string]any{
		"DemoInfo": builtin.Info{}, "ImageRef": catalog.ImageRef{}, "StepExpectation": catalog.StepExpectation{}, "ValidationIssue": catalog.ValidationIssue{}, "ExpectedImage": catalog.ExpectedImage{}, "ImageUpload": catalog.ImageUpload{}, "RequestHeader": catalog.RequestHeader{}, "HTTPHeaderSource": catalog.HTTPHeaderSource{}, "HeaderSourceResult": execution.HeaderSourceResult{}, "VariablesWrite": httpapi.VariablesWrite{}, "TestCaseCreate": httpapi.TestCaseCreate{},
		"Folder": catalog.Folder{}, "TestCase": catalog.TestCase{}, "Step": catalog.Step{}, "Block": catalog.Block{}, "KeyValue": catalog.KeyValue{}, "TypedValue": catalog.TypedValue{}, "Column": catalog.Column{}, "HTTPRequest": catalog.HTTPRequest{}, "GRPCRequest": catalog.GRPCRequest{}, "SQLRequest": catalog.SQLRequest{}, "Expectation": catalog.Expectation{},
		"Environment": resources.Environment{}, "Resource": resources.Resource{}, "ResourceConfig": resources.Config{}, "TLSConfig": resources.TLSConfig{}, "SecretChanges": resources.SecretChanges{},
		"Attempt": execution.Attempt{}, "ExecutionResult": execution.Result{}, "ComparisonResult": comparison.Result{}, "Difference": comparison.Difference{}, "Run": runs.Run{}, "StepState": runs.StepState{},
		"APIError": httpapi.Error{}, "CreateName": httpapi.CreateName{}, "ArchivePatch": httpapi.ArchivePatch{}, "TestCaseWrite": httpapi.TestCaseWrite{}, "EnvironmentWrite": httpapi.EnvironmentWrite{}, "ResourceWrite": httpapi.ResourceWrite{}, "ProtoBundle": httpapi.ProtoBundle{}, "RunStart": httpapi.RunStart{}, "AttemptStart": httpapi.AttemptStart{}, "StepWrite": httpapi.StepWrite{},
		"FolderPage": httpapi.FolderPage{}, "TestCasePage": httpapi.TestCasePage{}, "EnvironmentPage": httpapi.EnvironmentPage{}, "ResourcePage": httpapi.ResourcePage{}, "RunPage": httpapi.RunPage{},
	} {
		register(name, value)
	}
	for t := range names {
		schema(t)
	}
	schemas["Health"] = object{"type": "object", "required": []string{"status"}, "properties": object{"status": object{"type": "string", "enum": []string{"ready"}}}}
	schemas["Session"] = object{"type": "object", "required": []string{"csrfToken"}, "properties": object{"csrfToken": object{"type": "string"}}}
	setEnum("Block", "kind", "http", "grpc", "postgresql")
	setEnum("Resource", "kind", "http", "grpc", "postgresql")
	setEnum("ResourceWrite", "kind", "http", "grpc", "postgresql")
	setEnum("TypedValue", "type", "null", "string", "boolean", "integer", "decimal", "timestamp", "bytes", "json")
	setEnum("SQLRequest", "mode", "rows", "exec")
	setEnum("HTTPRequest", "bodyMode", "none", "text", "json")
	setEnum("Expectation", "bodyMode", "none", "text", "json", "rows", "exec")
	setEnum("TLSConfig", "mode", "tls", "mtls", "plaintext")
	setEnum("HTTPHeaderSource", "fetchMode", "per_attempt", "per_run")
	setEnum("HTTPHeaderSource", "bodyMode", "none", "text", "json")
	setEnum("Run", "status", "running", "passed", "failed", "blocked", "cancelled", "interrupted")
	setEnum("StepState", "status", "not_run", "passed", "failed", "blocked", "skipped")
	setEnum("StepWrite", "status", "not_run", "passed", "failed", "blocked", "skipped")
	setEnum("Attempt", "state", "queued", "running", "completed", "error", "timed_out", "cancelled", "interrupted")
	setEnum("ComparisonResult", "status", "match", "mismatch", "not_compared")
	paths := object{}
	add := func(method, path, id, summary, body, result string) {
		operation := object{"operationId": id, "summary": summary, "tags": []string{"qag"}, "responses": object{"200": response(result), "400": object{"$ref": "#/components/responses/BadRequest"}, "403": object{"$ref": "#/components/responses/Forbidden"}, "404": object{"$ref": "#/components/responses/NotFound"}, "409": object{"$ref": "#/components/responses/Conflict"}, "503": object{"$ref": "#/components/responses/Unavailable"}}}
		parameters := []object{}
		for _, match := range regexp.MustCompile(`\{([^}]+)\}`).FindAllStringSubmatch(path, -1) {
			parameters = append(parameters, object{"name": match[1], "in": "path", "required": true, "schema": object{"type": "string", "format": "uuid"}})
		}
		if strings.HasSuffix(result, "Page") {
			parameters = append(parameters, object{"name": "limit", "in": "query", "schema": object{"type": "integer", "minimum": 1, "maximum": 100, "default": 50}}, object{"name": "cursor", "in": "query", "schema": object{"type": "string"}})
		}
		if id == "listFolders" {
			parameters = append(parameters, object{"name": "archived", "in": "query", "schema": object{"type": "boolean", "default": false}})
		}
		if id == "listRuns" {
			parameters = append(parameters, object{"name": "retained", "in": "query", "schema": object{"type": "boolean"}})
			parameters = append(parameters, object{"name": "active", "in": "query", "description": "Вернуть живой прогон для восстановления браузерной вкладки", "schema": object{"type": "boolean"}})
		}
		if id == "startRun" || id == "startEditorAttempt" || id == "startRunAttempt" {
			parameters = append(parameters, object{"name": "Idempotency-Key", "in": "header", "required": true, "schema": object{"type": "string", "format": "uuid"}})
		}
		if len(parameters) > 0 {
			operation["parameters"] = parameters
		}
		if body != "" {
			operation["requestBody"] = request(body)
		}
		if method == "get" {
			operation["security"] = []any{}
		}
		if id == "readExpectedImage" {
			operation["responses"].(object)["200"] = object{"description": "Изображение-эталон", "content": object{"image/png": object{"schema": object{"type": "string", "format": "binary"}}, "image/jpeg": object{"schema": object{"type": "string", "format": "binary"}}, "image/webp": object{"schema": object{"type": "string", "format": "binary"}}}}
		}
		if method == "delete" {
			operation["responses"].(object)["200"] = object{"description": "Несохранённый прогон удалён", "content": object{"application/json": object{"schema": object{"type": "object", "properties": object{"deleted": object{"type": "boolean"}}, "required": []string{"deleted"}}}}}
		}
		if _, ok := paths[path]; !ok {
			paths[path] = object{}
		}
		paths[path].(object)[method] = operation
	}
	for _, route := range [][6]string{
		{"get", "/demo", "getDemo", "Получить встроенный пример", "", "DemoInfo"},
		{"get", "/session", "getSession", "Получить локальную сессию", "", "Session"},
		{"get", "/health", "getHealth", "Проверить готовность", "", "Health"},
		{"get", "/folders", "listFolders", "Список папок", "", "FolderPage"}, {"post", "/folders", "createFolder", "Создать папку", "CreateName", "Folder"}, {"patch", "/folders/{id}", "updateFolder", "Переименовать или архивировать папку", "ArchivePatch", "Folder"},
		{"get", "/folders/{id}/test-cases", "listTestCases", "Тест-кейсы папки", "", "TestCasePage"}, {"post", "/folders/{id}/test-cases", "createTestCase", "Создать тест-кейс", "TestCaseCreate", "TestCase"},
		{"get", "/test-cases/{id}", "getTestCase", "Прочитать сценарий", "", "TestCase"}, {"put", "/test-cases/{id}", "saveTestCase", "Сохранить версию сценария", "TestCaseWrite", "TestCase"}, {"patch", "/test-cases/{id}", "archiveTestCase", "Изменить имя или архив тест-кейса", "ArchivePatch", "TestCase"},
		{"post", "/test-cases/{id}/images", "uploadExpectedImage", "Загрузить эталон", "ImageUpload", "ExpectedImage"},
		{"get", "/test-cases/{id}/images/{imageId}", "readExpectedImage", "Прочитать эталон", "", "ExpectedImage"},
		{"put", "/environments/{id}/variables", "saveVariables", "Сохранить переменные", "VariablesWrite", "Environment"},
		{"get", "/environments", "listEnvironments", "Окружения", "", "EnvironmentPage"}, {"post", "/environments", "createEnvironment", "Создать окружение", "EnvironmentWrite", "Environment"}, {"put", "/environments/{id}", "saveEnvironment", "Сохранить окружение", "EnvironmentWrite", "Environment"},
		{"get", "/environments/{id}/resources", "listResources", "Ресурсы окружения", "", "ResourcePage"}, {"post", "/environments/{id}/resources", "createResource", "Создать ресурс", "ResourceWrite", "Resource"}, {"put", "/resources/{id}", "saveResource", "Сохранить новую версию ресурса", "ResourceWrite", "Resource"}, {"post", "/resources/{id}/proto-bundles", "uploadProtoBundle", "Загрузить proto-bundle", "ProtoBundle", "Resource"},
		{"post", "/test-cases/{id}/blocks/{blockId}/attempts", "startEditorAttempt", "Проверить блок из редактора", "AttemptStart", "Attempt"}, {"get", "/attempts/{id}", "getAttempt", "Получить состояние попытки", "", "Attempt"}, {"post", "/attempts/{id}/cancel", "cancelAttempt", "Отменить попытку", "", "Attempt"},
		{"get", "/test-cases/{id}/runs", "listRuns", "История прогонов", "", "RunPage"}, {"post", "/test-cases/{id}/runs", "startRun", "Начать прогон", "RunStart", "Run"}, {"get", "/runs/{id}", "getRun", "Прочитать снимок прогона", "", "Run"},
		{"patch", "/runs/{id}/steps/{stepId}", "updateRunStep", "Сохранить ручной статус и комментарий", "StepWrite", "Run"}, {"post", "/runs/{id}/blocks/{blockId}/attempts", "startRunAttempt", "Выполнить блок прогона", "", "Attempt"},
		{"post", "/runs/{id}/finish", "finishRun", "Завершить и оценить прогон", "", "Run"}, {"post", "/runs/{id}/cancel", "cancelRun", "Отменить прогон", "", "Run"}, {"post", "/runs/{id}/save", "saveRun", "Закрепить прогон вручную", "", "Run"}, {"delete", "/runs/{id}", "deleteRun", "Удалить несохранённый прогон", "", "Run"},
	} {
		add(route[0], route[1], route[2], route[3], route[4], route[5])
	}
	responses := object{}
	for _, name := range []string{"BadRequest", "Forbidden", "NotFound", "Conflict", "Unavailable"} {
		responses[name] = object{"description": name, "content": object{"application/json": object{"schema": ref("APIError")}}}
	}
	doc := object{"openapi": "3.1.0", "info": object{"title": "qag API", "version": "0.1.0", "description": "Локальный API ручных проверок. Схемы генерируются из Go DTO; секреты выдаются только write-only.", "license": object{"name": "Project license", "url": "https://github.com/SardelkaS/qag/blob/main/LICENSE"}}, "servers": []object{{"url": "http://127.0.0.1:8080/api/v1"}}, "tags": []object{{"name": "qag", "description": "Каталог, ресурсы, выполнение и история"}}, "security": []object{{"LocalSession": []string{}}}, "paths": paths, "components": object{"schemas": schemas, "responses": responses, "securitySchemes": object{"LocalSession": object{"type": "apiKey", "in": "header", "name": "X-QAG-CSRF", "description": "Токен из GET /session; изменяющие команды также требуют правильный Origin и application/json."}}}}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		panic(err)
	}
	if err = os.MkdirAll("api", 0755); err != nil {
		panic(err)
	}
	if err = os.WriteFile("api/openapi.json", append(data, '\n'), 0644); err != nil {
		panic(err)
	}
	fmt.Println("api/openapi.json generated from Go DTOs")
}

func setEnum(name, field string, values ...string) {
	schemas[name].(object)["properties"].(object)[field].(object)["enum"] = values
}
