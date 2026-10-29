package reporting

import (
	"encoding/json"
	"fmt"
	"qag/internal/catalog"
	"qag/internal/runs"
	"strings"
)

func StepLabel(status string) string {
	switch status {
	case "passed":
		return "Пройден"
	case "failed":
		return "Не пройден"
	default:
		return "Не проверен"
	}
}
func Value(v catalog.TypedValue) string {
	if v.Type == "null" {
		return "NULL"
	}
	if v.Type == "string" && v.Value == "" {
		return `""`
	}
	return v.Value
}
func TableLines(node runs.ReportNode) []string {
	columns := []string{}
	for _, c := range node.Columns {
		columns = append(columns, c.Name+" ("+c.Type+")")
	}
	lines := []string{strings.Join(columns, " | ")}
	for _, row := range node.Rows {
		cells := []string{}
		for _, v := range row {
			cells = append(cells, Value(v))
		}
		lines = append(lines, strings.Join(cells, " | "))
	}
	return lines
}
func Plain(report runs.Report) string {
	var out strings.Builder
	for _, n := range report.Document {
		text := n.Text
		if n.Status != "" {
			text += " — " + StepLabel(n.Status)
		}
		if n.Kind == "table" {
			text = strings.Join(TableLines(n), "\n")
		}
		if n.Kind == "image" {
			text = "[Эталон: " + n.Text + "; " + n.ImageID + "]"
		}
		out.WriteString(text)
		out.WriteString("\n\n")
	}
	return strings.TrimSpace(out.String())
}

// BuildDocument is the sole semantic projection used by all export renderers.
// Every string remains text; protocol bodies are never decoded as JS numbers.
func BuildDocument(report runs.Report) []runs.ReportNode {
	nodes := []runs.ReportNode{}
	group := ""
	add := func(kind, text string, level int, status string) {
		nodes = append(nodes, runs.ReportNode{ID: fmt.Sprintf("node-%d", len(nodes)+1), Kind: kind, Text: text, Level: level, Status: status, CallID: group})
	}
	code := func(text string) {
		if text != "" {
			add("code", text, 0, "")
		}
	}
	pairs := func(title string, values []catalog.KeyValue) {
		if len(values) == 0 {
			return
		}
		lines := []string{title}
		for _, v := range values {
			lines = append(lines, v.Name+": "+v.Value)
		}
		code(strings.Join(lines, "\n"))
	}
	headers := func(title string, values []catalog.RequestHeader) {
		kv := []catalog.KeyValue{}
		for _, v := range values {
			kv = append(kv, catalog.KeyValue{Name: v.Name, Value: v.Value})
		}
		pairs(title, kv)
	}
	table := func(columns []catalog.Column, rows [][]catalog.TypedValue) {
		if len(columns) > 0 {
			nodes = append(nodes, runs.ReportNode{ID: fmt.Sprintf("node-%d", len(nodes)+1), Kind: "table", CallID: group, Columns: columns, Rows: rows})
		}
	}
	images := func(refs []catalog.ImageRef) {
		for _, ref := range refs {
			nodes = append(nodes, runs.ReportNode{ID: fmt.Sprintf("node-%d", len(nodes)+1), Kind: "image", Text: ref.Caption, ImageID: ref.ImageID, CallID: group})
		}
	}
	request := func(r runs.ReportRequest, resource string) {
		add("text", fmt.Sprintf("Ресурс: %s · тайм-аут: %d сек. (0 — по умолчанию)", resource, r.TimeoutSeconds), 0, "")
		if r.HTTP != nil {
			h := r.HTTP
			code(h.Method + " " + h.Path)
			pairs("Query", h.Query)
			headers("Заголовки", h.Headers)
			if h.BodyMode != "none" {
				code(h.Body)
			}
		}
		if r.GRPC != nil {
			g := r.GRPC
			code(g.Service + "/" + g.Method)
			headers("Метаданные", g.Metadata)
			code(g.Body)
		}
		if r.SQL != nil {
			s := r.SQL
			code(s.Query)
			for i, v := range s.Parameters {
				code(fmt.Sprintf("$%d (%s) = %s", i+1, v.Type, Value(v)))
			}
			add("text", "Режим SQL: "+s.Mode, 0, "")
		}
	}
	expected := func(e *catalog.Expectation) {
		if e == nil {
			return
		}
		add("heading", "Ожидаемый результат", 4, "")
		if e.StatusCode != nil {
			code(fmt.Sprintf("HTTP %d", *e.StatusCode))
		}
		if e.GRPCCode != "" {
			code("gRPC " + e.GRPCCode)
		}
		pairs("Ожидаемые заголовки", e.Headers)
		code(e.Body)
		table(e.Columns, e.Rows)
		if e.RowsAffected != nil {
			code("Ожидаемое число строк: " + *e.RowsAffected)
		}
		images(e.Images)
	}
	attempt := func(a runs.ReportAttempt, label, resource string) {
		group = "attempt:" + a.ID
		add("heading", fmt.Sprintf("%s · попытка %d · %s", label, a.Number, a.State), 3, "")
		add("text", fmt.Sprintf("%s · %d мс · версия запроса %s", a.StartedAt.Format("2006-01-02 15:04:05 MST"), a.DurationMS, a.VariantVersionID), 0, "")
		request(a.Request, resource)
		if a.VariantVersionID == "" {
			expected(a.Expected)
		}
		if a.Result == nil {
			add("text", "Подтверждённого результата нет", 0, "")
			return
		}
		r := a.Result
		add("heading", "Фактический результат", 4, "")
		if r.StatusCode != nil {
			code(fmt.Sprintf("HTTP %d", *r.StatusCode))
		}
		if r.GRPCCode != "" {
			code("gRPC " + r.GRPCCode + " " + r.StatusMessage)
		}
		pairs("Заголовки", r.Headers)
		pairs("Trailers", r.Trailers)
		code(r.Body)
		table(r.Columns, r.Rows)
		if r.RowsAffected != nil {
			code("Затронуто строк: " + *r.RowsAffected)
		}
		if r.Error != "" {
			code("Ошибка: " + r.Error)
		}
		if r.Truncated {
			add("text", "Ответ усечён исполнителем", 0, "")
		}
		if r.Redacted {
			add("text", "Секретные значения скрыты", 0, "")
		}
		if a.VariantVersionID == "" {
			add("text", "Сравнение: "+r.Comparison.Status, 0, "")
			code(r.Comparison.Reason)
			for _, d := range r.Comparison.Differences {
				code(d.Path + "\nОжидалось: " + d.Expected + "\nПолучено: " + d.Actual)
			}
		} else {
			add("text", "Исследовательский вызов — без сравнения", 0, "")
		}
		for _, trace := range r.HeaderSources {
			raw, _ := json.Marshal(trace)
			code("Источник заголовка: " + string(raw))
		}
	}
	add("heading", report.Title, 1, "")
	partial := "Завершённый прогон"
	if report.Partial {
		partial = "Частичный отчёт текущего прогона"
	}
	add("text", partial+" · "+report.Status+"\nОкружение: "+report.EnvironmentName+"\nСрез: "+report.CreatedAt.Format("2006-01-02 15:04:05 MST")+"\nПрогон: "+report.RunID, 0, "")
	for i, s := range report.Steps {
		group = ""
		title := s.Title
		if title == "" {
			title = fmt.Sprintf("Шаг %d", i+1)
		}
		add("heading", fmt.Sprintf("%d. %s", i+1, title), 2, s.Status)
		add("text", s.Description, 0, "")
		if s.LegacyStatus != "" {
			add("text", "Исторический статус: "+s.LegacyStatus, 0, "")
		}
		if s.Comment != "" {
			add("text", "Комментарий: "+s.Comment, 0, "")
		}
		if s.Expected != nil {
			add("heading", "Ожидаемый результат шага", 3, "")
			add("text", s.Expected.Text, 0, "")
			images(s.Expected.Images)
		}
		for j, c := range s.Calls {
			group = "original:" + c.ID
			label := fmt.Sprintf("Вызов %d · %s · Исходный", j+1, c.Kind)
			add("heading", label, 3, "")
			request(c.Request, c.Resource)
			expected(c.Expected)
			for _, a := range c.Attempts {
				attempt(a, label, c.Resource)
			}
			for _, v := range c.Variants {
				for _, version := range v.Versions {
					group = "version:" + version.ID
					add("heading", fmt.Sprintf("%s · Исследовательский · версия %d", v.Name, version.Revision), 3, "")
					request(version.Request, c.Resource)
				}
				for _, a := range v.Attempts {
					attempt(a, v.Name+" · Исследовательский", c.Resource)
				}
			}
		}
	}
	return nodes
}
