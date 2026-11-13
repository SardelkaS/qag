package reporting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/ports"
	"github.com/SardelkaS/qag/internal/problem"
)

type CaseExport struct {
	TestCaseID string `json:"testCaseId"`
	Revision   int64  `json:"revision"`
	Format     string `json:"format"`
	Text       string `json:"text"`
	SHA256     string `json:"sha256"`
}

var wikiMacro = regexp.MustCompile(`(?i)\{(?:code|noformat)(?:[:}])`)
var wikiEscaper = strings.NewReplacer("\\", "\\\\", "{", "\\{", "}", "\\}", "[", "\\[", "]", "\\]", "|", "\\|", "*", "\\*", "_", "\\_", "#", "\\#", "!", "\\!", "+", "\\+", "-", "\\-", "~", "\\~", "^", "\\^", "&", "&amp;", "<", "&lt;", ">", "&gt;")

func wikiText(value string) string {
	return strings.ReplaceAll(wikiEscaper.Replace(strings.ReplaceAll(value, "\r\n", "\n")), "\n", "\\\\\n")
}
func wikiCode(value string) string {
	if value == "" {
		return "(не заполнено)"
	}
	// No untrusted closing macro is ever placed inside a macro. The fallback
	// uses escaped ordinary wiki text, retaining line breaks and literal braces.
	if wikiMacro.MatchString(value) {
		return wikiText(value)
	}
	return "{noformat}\n" + value + "\n{noformat}"
}
func sensitiveHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key":
		return true
	}
	return false
}
func safeHeaders(headers []catalog.RequestHeader) {
	for i := range headers {
		h := &headers[i]
		if sensitiveHeader(h.Name) && !strings.Contains(h.Value, "${") {
			h.Value = "[скрыто]"
		}
		if h.Source != nil {
			// Inline source definitions may contain connection certificates. They
			// are not needed to describe the test case and must not enter a comment.
			h.Source.TLS = catalog.TLSConfig{Mode: h.Source.TLS.Mode}
			for j := range h.Source.Headers {
				v := &h.Source.Headers[j]
				if sensitiveHeader(v.Name) && !strings.Contains(v.Value, "${") {
					v.Value = "[скрыто]"
				}
			}
		}
	}
}

func ExportCase(ctx context.Context, repo ports.Catalog, id string, revision int64, includeExpected bool) (CaseExport, error) {
	c, err := repo.GetCase(ctx, id)
	if err != nil {
		return CaseExport{}, err
	}
	if revision < 1 {
		return CaseExport{}, problem.Invalid("revision", "Нужна сохранённая версия сценария")
	}
	if c.Revision != revision {
		return CaseExport{}, problem.Conflict("Сценарий изменился; обновите его перед экспортом")
	}
	raw, _ := json.Marshal(c)
	var definition catalog.TestCase
	_ = json.Unmarshal(raw, &definition)
	c = definition // never reuse slices owned by a caller's definition
	var out strings.Builder
	heading := func(level int, text string) {
		fmt.Fprintf(&out, "h%d. %s\n\n", level, wikiText(strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))))
	}
	text := func(value string) { out.WriteString(value); out.WriteString("\n\n") }
	heading(1, c.Name)
	imageNames := map[string]string{}
	images := func(refs []catalog.ImageRef) error {
		for _, ref := range refs {
			name, ok := imageNames[ref.ImageID]
			if !ok {
				meta, _, e := repo.ReadImage(ctx, c.ID, ref.ImageID)
				if e != nil {
					return e
				}
				name = meta.Filename
				imageNames[ref.ImageID] = name
			}
			label := name
			if ref.Caption != "" {
				label += " — " + ref.Caption
			}
			text(wikiText("Эталон: " + label + ". Приложите изображение к Jira вручную."))
		}
		return nil
	}
	jsonCode := func(v any) { raw, _ := json.MarshalIndent(v, "", "  "); text(wikiCode(string(raw))) }
	for i, step := range c.Steps {
		if err := ctx.Err(); err != nil {
			return CaseExport{}, err
		}
		title := step.Title
		if title == "" {
			title = "Шаг"
		}
		heading(2, fmt.Sprintf("%d. %s", i+1, title))
		text(wikiText(step.Description))
		if includeExpected && step.Expected != nil {
			heading(3, "Ожидаемый результат шага")
			text(wikiText(step.Expected.Text))
			if err = images(step.Expected.Images); err != nil {
				return CaseExport{}, err
			}
		}
		for j, b := range step.Blocks {
			heading(3, fmt.Sprintf("Вызов %d: %s — %s", j+1, b.Kind, b.Resource))
			if b.HTTP != nil {
				safeHeaders(b.HTTP.Headers)
				jsonCode(b.HTTP)
			} else if b.GRPC != nil {
				safeHeaders(b.GRPC.Metadata)
				jsonCode(b.GRPC)
			} else if b.SQL != nil {
				jsonCode(b.SQL)
			} else {
				text("(запрос не заполнен)")
			}
			if includeExpected {
				heading(4, "Ожидаемый результат вызова")
				for k := range b.Expected.Headers {
					h := &b.Expected.Headers[k]
					if sensitiveHeader(h.Name) && !strings.Contains(h.Value, "${") {
						h.Value = "[скрыто]"
					}
				}
				refs := b.Expected.Images
				b.Expected.Images = nil
				jsonCode(b.Expected)
				if err = images(refs); err != nil {
					return CaseExport{}, err
				}
			}
		}
	}
	value := strings.TrimSpace(out.String()) + "\n"
	if len(value) > 32<<20 {
		return CaseExport{}, problem.Invalid("testCase", "Сценарий слишком большой для экспорта Jira")
	}
	sum := sha256.Sum256([]byte(value))
	return CaseExport{TestCaseID: c.ID, Revision: c.Revision, Format: "jira-wiki", Text: value, SHA256: hex.EncodeToString(sum[:])}, nil
}
