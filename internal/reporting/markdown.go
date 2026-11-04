package reporting

import (
	"fmt"
	"strings"

	"github.com/SardelkaS/qag/internal/problem"
	"github.com/SardelkaS/qag/internal/runs"
)

func escapeMarkdown(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune("\\`*_{}[]()#+-.!|<>", r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
func Markdown(report runs.Report) (string, error) {
	parts := []string{}
	for _, n := range report.Document {
		text := n.Text
		if n.Status != "" {
			text += " — " + StepLabel(n.Status)
		}
		switch n.Kind {
		case "table", "code":
			if n.Kind == "table" {
				text = strings.Join(TableLines(n), "\n")
			}
			maxRun, current := 0, 0
			for _, r := range text {
				if r == '`' {
					current++
					maxRun = max(maxRun, current)
				} else {
					current = 0
				}
			}
			fence := strings.Repeat("`", max(3, maxRun+1))
			text = fence + "\n" + text + "\n" + fence
		case "image":
			found := false
			for _, im := range report.Images {
				if im.ID == n.ImageID {
					label := n.Text
					if label == "" {
						label = "Эталон"
					}
					text = fmt.Sprintf("![%s](data:%s;base64,%s)", escapeMarkdown(label), im.MIME, im.Data)
					found = true
					break
				}
			}
			if !found {
				return "", problem.Invalid("image", "Эталон недоступен")
			}
		case "heading":
			text = strings.Repeat("#", min(6, max(1, n.Level))) + " " + strings.ReplaceAll(escapeMarkdown(text), "\n", " ")
		default:
			text = escapeMarkdown(text)
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n\n"), nil
}
