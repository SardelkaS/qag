package catalog

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"qag/internal/problem"
)

var idPattern = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)
var resourcePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
var methodPattern = regexp.MustCompile(`^[A-Z]+$`)
var decimalPattern = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)
var headerPattern = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
var grpcCodes = map[string]bool{"OK": true, "CANCELLED": true, "UNKNOWN": true, "INVALID_ARGUMENT": true, "DEADLINE_EXCEEDED": true, "NOT_FOUND": true, "ALREADY_EXISTS": true, "PERMISSION_DENIED": true, "RESOURCE_EXHAUSTED": true, "FAILED_PRECONDITION": true, "ABORTED": true, "OUT_OF_RANGE": true, "UNIMPLEMENTED": true, "INTERNAL": true, "UNAVAILABLE": true, "DATA_LOSS": true, "UNAUTHENTICATED": true}

func NewID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic("system random source unavailable")
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", bytes[:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:])
}
func ValidID(id string) bool { return idPattern.MatchString(id) }
func Name(value string) (string, string, error) {
	name := norm.NFC.String(strings.TrimSpace(value))
	if name == "" || utf8.RuneCountInString(name) > 200 {
		return "", "", problem.Invalid("name", "Введите имя длиной от 1 до 200 символов")
	}
	return name, cases.Fold().String(name), nil
}

func ValidateSteps(steps []Step) error {
	seen := map[string]bool{}
	for i := range steps {
		s := &steps[i]
		prefix := fmt.Sprintf("steps.%d", i)
		if !ValidID(s.ID) || seen[s.ID] {
			return problem.Invalid(prefix+".id", "Нужен уникальный идентификатор шага")
		}
		seen[s.ID] = true
		if strings.TrimSpace(s.Description) == "" {
			return problem.Invalid(prefix+".description", "Добавьте описание шага")
		}
		if s.Blocks == nil {
			s.Blocks = []Block{}
		}
		for j := range s.Blocks {
			b := &s.Blocks[j]
			field := fmt.Sprintf("%s.blocks.%d", prefix, j)
			if !ValidID(b.ID) || seen[b.ID] {
				return problem.Invalid(field+".id", "Нужен уникальный идентификатор блока")
			}
			seen[b.ID] = true
			if err := ValidateBlock(b, field); err != nil {
				return err
			}
		}
	}
	return nil
}

func ValidateBlock(b *Block, field string) error {
	invalid := func(s, message string) error { return problem.Invalid(field+"."+s, message) }
	if !resourcePattern.MatchString(b.Resource) {
		return invalid("resource", "Выберите именованный ресурс, например AI_GW_SERVISE")
	}
	if b.TimeoutSeconds == 0 {
		b.TimeoutSeconds = 30
	}
	if b.TimeoutSeconds < 1 || b.TimeoutSeconds > 300 {
		return invalid("timeoutSeconds", "Тайм-аут должен быть от 1 до 300 секунд")
	}
	count := 0
	for _, present := range []bool{b.HTTP != nil, b.GRPC != nil, b.SQL != nil} {
		if present {
			count++
		}
	}
	if count != 1 {
		return invalid("kind", "Блок должен содержать параметры ровно одного протокола")
	}
	switch b.Kind {
	case "http":
		if b.HTTP == nil {
			return invalid("http", "Нужны параметры HTTP")
		}
		r := b.HTTP
		if !methodPattern.MatchString(r.Method) {
			return invalid("http.method", "Укажите HTTP-метод заглавными буквами")
		}
		if strings.Contains(r.Path, "://") || strings.HasPrefix(r.Path, "//") {
			return invalid("http.path", "Укажите путь относительно ресурса")
		}
		if err := validateHeaders(r.Headers); err != nil {
			return invalid("http.headers", err.Error())
		}
		if r.BodyMode != "none" && r.BodyMode != "text" && r.BodyMode != "json" {
			return invalid("http.bodyMode", "Выберите формат тела")
		}
		if r.BodyMode == "json" && !json.Valid([]byte(r.Body)) {
			return invalid("http.body", "Некорректный JSON запроса")
		}
		if b.Expected.StatusCode == nil || *b.Expected.StatusCode < 100 || *b.Expected.StatusCode > 599 {
			return invalid("expected.statusCode", "Укажите ожидаемый HTTP-статус")
		}
		if err := validateHeaders(b.Expected.Headers); err != nil {
			return invalid("expected.headers", err.Error())
		}
	case "grpc":
		if b.GRPC == nil {
			return invalid("grpc", "Нужны параметры gRPC")
		}
		if strings.TrimSpace(b.GRPC.Service) == "" || strings.TrimSpace(b.GRPC.Method) == "" || strings.ContainsAny(b.GRPC.Service+b.GRPC.Method, "/\r\n ") {
			return invalid("grpc.method", "Укажите полное имя сервиса и unary-метод")
		}
		if !json.Valid([]byte(b.GRPC.Body)) {
			return invalid("grpc.body", "Некорректный JSON сообщения")
		}
		if !grpcCodes[b.Expected.GRPCCode] {
			return invalid("expected.grpcCode", "Укажите ожидаемый gRPC-код")
		}
		if err := validateHeaders(b.GRPC.Metadata); err != nil {
			return invalid("grpc.metadata", err.Error())
		}
	case "postgresql":
		if b.SQL == nil {
			return invalid("sql", "Нужны параметры PostgreSQL")
		}
		if strings.TrimSpace(b.SQL.Query) == "" || strings.Contains(b.SQL.Query, "${") {
			return invalid("sql.query", "Укажите SQL с $1-параметрами; шаблоны в SQL-тексте не поддерживаются")
		}
		if b.SQL.Mode != "rows" && b.SQL.Mode != "exec" {
			return invalid("sql.mode", "Выберите режим Строки или Команда")
		}
		for i, value := range b.SQL.Parameters {
			if strings.Contains(value.Value, "${") && value.Type != "null" {
				if err := ValidateValue(TypedValue{Type: value.Type, Value: validPlaceholderValue(value.Type)}); err != nil {
					return invalid(fmt.Sprintf("sql.parameters.%d", i), err.Error())
				}
				continue
			}
			if err := ValidateValue(value); err != nil {
				return invalid(fmt.Sprintf("sql.parameters.%d", i), err.Error())
			}
		}
		if b.SQL.Mode == "exec" {
			if b.Expected.BodyMode != "exec" || b.Expected.RowsAffected == nil {
				return invalid("expected.rowsAffected", "Укажите ожидаемое число затронутых строк")
			}
			n, ok := new(big.Int).SetString(*b.Expected.RowsAffected, 10)
			if !ok || n.Sign() < 0 {
				return invalid("expected.rowsAffected", "Ожидается неотрицательное целое число")
			}
		} else {
			if b.Expected.BodyMode != "rows" {
				return invalid("expected.bodyMode", "Настройте ожидание строк PostgreSQL")
			}
			for _, row := range b.Expected.Rows {
				if len(row) != len(b.Expected.Columns) {
					return invalid("expected.rows", "Число ячеек должно совпадать с числом колонок")
				}
				for _, value := range row {
					if err := ValidateValue(value); err != nil {
						return invalid("expected.rows", err.Error())
					}
				}
			}
		}
	default:
		return invalid("kind", "Поддерживаются HTTP, gRPC и PostgreSQL")
	}
	if b.Kind != "postgresql" {
		if b.Expected.BodyMode != "none" && b.Expected.BodyMode != "json" && b.Expected.BodyMode != "text" {
			return invalid("expected.bodyMode", "Выберите формат ожидаемого тела")
		}
		if b.Expected.BodyMode == "json" && !json.Valid([]byte(b.Expected.Body)) {
			return invalid("expected.body", "Некорректный ожидаемый JSON")
		}
	}
	return nil
}

func ValidateHeaders(headers []KeyValue) error {
	for _, h := range headers {
		if !headerPattern.MatchString(h.Name) || strings.ContainsAny(h.Value, "\r\n") {
			return fmt.Errorf("Некорректное имя либо значение заголовка")
		}
	}
	return nil
}

func validateHeaders(headers []KeyValue) error { return ValidateHeaders(headers) }

func validPlaceholderValue(kind string) string {
	switch kind {
	case "boolean":
		return "true"
	case "integer", "decimal":
		return "0"
	case "timestamp":
		return "2000-01-01T00:00:00Z"
	case "json":
		return "null"
	}
	return ""
}

func ValidateValue(value TypedValue) error {
	valid := true
	switch value.Type {
	case "null":
		valid = value.Value == ""
	case "string":
	case "boolean":
		valid = value.Value == "true" || value.Value == "false"
	case "integer":
		_, valid = new(big.Int).SetString(value.Value, 10)
	case "decimal":
		_, valid = new(big.Rat).SetString(value.Value)
		valid = valid && decimalPattern.MatchString(value.Value)
	case "timestamp":
		_, err := time.Parse(time.RFC3339Nano, value.Value)
		valid = err == nil
	case "json":
		valid = json.Valid([]byte(value.Value))
	case "bytes":
		_, err := base64.StdEncoding.DecodeString(value.Value)
		valid = err == nil
	default:
		valid = false
	}
	if !valid {
		return fmt.Errorf("Некорректное значение типа %s", strconv.Quote(value.Type))
	}
	return nil
}
