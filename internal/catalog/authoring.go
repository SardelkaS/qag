package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/SardelkaS/qag/internal/problem"
	"strings"
)

type ImageRef struct {
	ImageID string `json:"imageId"`
	Caption string `json:"caption,omitempty"`
}
type StepExpectation struct {
	Text   string     `json:"text,omitempty"`
	Images []ImageRef `json:"images,omitempty"`
}
type ValidationIssue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}
type TLSConfig struct {
	Mode        string `json:"mode"`
	ServerName  string `json:"serverName,omitempty"`
	CA          string `json:"ca,omitempty"`
	Certificate string `json:"certificate,omitempty"`
}
type RequestHeader struct {
	ID     string            `json:"id,omitempty"`
	Name   string            `json:"name"`
	Value  string            `json:"value,omitempty"`
	Source *HTTPHeaderSource `json:"source,omitempty"`
}
type HTTPHeaderSource struct {
	Method           string     `json:"method"`
	URL              string     `json:"url"`
	Query            []KeyValue `json:"query"`
	Headers          []KeyValue `json:"headers"`
	BodyMode         string     `json:"bodyMode"`
	Body             string     `json:"body"`
	TLS              TLSConfig  `json:"tls"`
	PrivateKeySecret string     `json:"privateKeySecret,omitempty"`
	JSONPointer      string     `json:"jsonPointer"`
	Prefix           string     `json:"prefix,omitempty"`
	FetchMode        string     `json:"fetchMode"`
}

// Draft validation enforces document integrity, never completeness of user fields.
func ValidateDraft(steps []Step) error {
	data, err := json.Marshal(steps)
	if err != nil || len(data) > 24<<20 {
		return problem.Invalid("steps", "Сценарий слишком большой")
	}
	if len(steps) > 500 {
		return problem.Invalid("steps", "Не больше 500 шагов")
	}
	seen := map[string]bool{}
	checkID := func(id, path string) error {
		if !ValidID(id) || seen[id] {
			return problem.Invalid(path, "Нужен уникальный идентификатор")
		}
		seen[id] = true
		return nil
	}
	images := func(refs []ImageRef, path string) error {
		if len(refs) > 10 {
			return problem.Invalid(path, "Не больше 10 изображений в ожидании")
		}
		for _, ref := range refs {
			if !ValidID(ref.ImageID) || len(ref.Caption) > 4000 {
				return problem.Invalid(path, "Некорректная ссылка или слишком длинная подпись")
			}
		}
		return nil
	}
	for i, s := range steps {
		p := fmt.Sprintf("steps.%d", i)
		if err := checkID(s.ID, p+".id"); err != nil {
			return err
		}
		if s.Expected != nil {
			if err := images(s.Expected.Images, p+".expected.images"); err != nil {
				return err
			}
		}
		if len(s.Blocks) > 100 {
			return problem.Invalid(p+".blocks", "Не больше 100 вызовов в шаге")
		}
		for j, b := range s.Blocks {
			bp := fmt.Sprintf("%s.blocks.%d", p, j)
			if err := checkID(b.ID, bp+".id"); err != nil {
				return err
			}
			if b.Kind != "http" && b.Kind != "grpc" && b.Kind != "postgresql" {
				return problem.Invalid(bp+".kind", "Неизвестный протокол")
			}
			if b.HTTP != nil && b.Kind != "http" || b.GRPC != nil && b.Kind != "grpc" || b.SQL != nil && b.Kind != "postgresql" {
				return problem.Invalid(bp+".kind", "Параметры не соответствуют протоколу")
			}
			var hs []RequestHeader
			if b.HTTP != nil {
				hs = b.HTTP.Headers
			}
			if b.GRPC != nil {
				hs = b.GRPC.Metadata
			}
			for k, h := range hs {
				if h.Source != nil {
					if err := checkID(h.ID, fmt.Sprintf("%s.headers.%d.id", bp, k)); err != nil {
						return err
					}
				}
			}
			if err := images(b.Expected.Images, bp+".expected.images"); err != nil {
				return err
			}
		}
	}
	return nil
}
func ValidationIssues(steps []Step) []ValidationIssue {
	issues := []ValidationIssue{}
	if len(steps) == 0 {
		return append(issues, ValidationIssue{"steps", "Добавьте хотя бы один содержательный шаг"})
	}
	for i, s := range steps {
		p := fmt.Sprintf("steps.%d", i)
		if strings.TrimSpace(s.Description) == "" && len(s.Blocks) == 0 && (s.Expected == nil || (strings.TrimSpace(s.Expected.Text) == "" && len(s.Expected.Images) == 0)) {
			issues = append(issues, ValidationIssue{p + ".description", "Заполните действие или ожидаемый результат шага"})
		}
		for j, b := range s.Blocks {
			data, _ := json.Marshal(b)
			var copy Block
			_ = json.Unmarshal(data, &copy)
			if err := ValidateBlock(&copy, fmt.Sprintf("%s.blocks.%d", p, j)); err != nil {
				var e *problem.Error
				if errors.As(err, &e) {
					for path, message := range e.Fields {
						issues = append(issues, ValidationIssue{path, message})
					}
				}
			}
		}
	}
	return issues
}
func ValidateExecutable(steps []Step) error {
	if err := ValidateDraft(steps); err != nil {
		return err
	}
	if issues := ValidationIssues(steps); len(issues) > 0 {
		fields := map[string]string{}
		for _, i := range issues {
			fields[i.Path] = i.Message
		}
		return &problem.Error{Code: "invalid", Message: "Сценарий не готов к запуску", Fields: fields}
	}
	return nil
}

func MergeRequestHeaders(base, override []RequestHeader) []RequestHeader {
	names := map[string]bool{}
	for _, h := range override {
		names[strings.ToLower(h.Name)] = true
	}
	result := []RequestHeader{}
	for _, h := range base {
		if !names[strings.ToLower(h.Name)] {
			result = append(result, h)
		}
	}
	return append(result, override...)
}
