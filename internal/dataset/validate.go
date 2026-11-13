package dataset

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/resources"
	"github.com/SardelkaS/qag/internal/runs"
)

func Validate(doc Document) error {
	if doc.Format != "qag" || doc.Version < 1 || doc.Version > Version {
		return invalid("формат", "", "неподдерживаемая версия")
	}
	d := doc.Data
	v := reflect.ValueOf(d)
	for i := 0; i < v.NumField(); i++ {
		s := v.Field(i)
		group := v.Type().Field(i).Tag.Get("yaml")
		if s.Len() > MaxRecords {
			return invalid(group, "", "слишком много записей")
		}
		seen := map[string]bool{}
		for j := 0; j < s.Len(); j++ {
			r := s.Index(j)
			k := key(r)
			if seen[k] {
				return invalid(group, "", "повторный ID")
			}
			seen[k] = true
			for f := 0; f < r.NumField(); f++ {
				field := r.Field(f)
				name := r.Type().Field(f).Name
				if strings.HasSuffix(name, "ID") && field.Kind() == reflect.String {
					id := field.String()
					optional := name == "RunID" && (group == "callAttempts" || group == "callLogs") || name == "VariantID" || name == "VariantVersionID" || name == "BatchID" && group == "callAttempts" || name == "AttemptID" || name == "ProbeID" && group == "callLogs" || name == "TestCaseID" && group == "callLogs" || name == "ResourceID" && (group == "callLogs" || group == "httpVariableProbes") || name == "StepID" && group == "runBatches"
					if id == "" && optional {
						continue
					}
					if group == "demoItems" && name == "ID" {
						if id == "" || len(id) > 128 {
							return invalid(group, "", "ID")
						}
						continue
					}
					if !catalog.ValidID(id) || id != strings.ToLower(id) {
						return invalid(group, "", "ID")
					}
				}
				if (name == "Revision" || name == "CaseRevision") && field.Int() < 1 {
					return invalid(group, k, "revision")
				}
				if field.Type() == reflect.TypeFor[JSON]() {
					raw := field.String()
					if raw == "" && name == "Result" {
						continue
					}
					if !json.Valid([]byte(raw)) {
						return invalid(group, k, "JSON")
					}
				}
			}
		}
	}
	unique := map[string]bool{}
	claim := func(kind, value string) error {
		k := kind + "/" + value
		if unique[k] {
			return invalid(kind, "", "конфликт уникальности")
		}
		unique[k] = true
		return nil
	}
	name := func(kind, id, value, normalized string) error {
		n, norm, e := catalog.Name(value)
		if e != nil || n != value || norm != normalized {
			return invalid(kind, id, "имя")
		}
		return nil
	}
	folders := map[string]Folder{}
	cases := map[string]TestCase{}
	envs := map[string]Environment{}
	evs := map[string]EnvironmentVersion{}
	rs := map[string]Resource{}
	rvs := map[string]ResourceVersion{}
	rr := map[string]Run{}
	images := map[string]Image{}
	vs := map[string]Variant{}
	vvs := map[string]VariantVersion{}
	batches := map[string]Batch{}
	attempts := map[string]CallAttempt{}
	for _, f := range d.Folders {
		if err := name("folders", f.ID, f.Name, f.NormalizedName); err != nil {
			return err
		}
		if err := claim("folderName", f.NormalizedName); err != nil {
			return err
		}
		folders[f.ID] = f
	}
	for _, c := range d.TestCases {
		if _, ok := folders[c.FolderID]; !ok {
			return invalid("testCases", c.ID, "папка")
		}
		if err := name("testCases", c.ID, c.Name, c.NormalizedName); err != nil {
			return err
		}
		if err := claim("caseName", c.FolderID+"/"+c.NormalizedName); err != nil {
			return err
		}
		steps, e := Value[[]catalog.Step](c.Steps)
		if e != nil || catalog.ValidateDraft(steps) != nil {
			return invalid("testCases", c.ID, "шаги")
		}
		cases[c.ID] = c
	}
	for _, e := range d.Environments {
		if err := name("environments", e.ID, e.Name, e.NormalizedName); err != nil {
			return err
		}
		if err := claim("environmentName", e.NormalizedName); err != nil {
			return err
		}
		envs[e.ID] = e
	}
	for _, e := range d.EnvironmentVersions {
		if _, ok := envs[e.EnvironmentID]; !ok {
			return invalid("environmentVersions", e.ID, "окружение")
		}
		if err := resources.EnvironmentVariableContext(e.Variables, e.HTTPVariables, e.Secrets); err != nil {
			return invalid("environmentVersions", e.ID, "переменные")
		}
		if !sameNames(e.SecretNames, e.Secrets) {
			return invalid("environmentVersions", e.ID, "имена секретов")
		}
		if err := claim("environmentRevision", fmt.Sprint(e.EnvironmentID, "/", e.Revision)); err != nil {
			return err
		}
		evs[e.ID] = e
	}
	for _, e := range d.Environments {
		v, ok := evs[e.CurrentVersionID]
		if !ok || v.EnvironmentID != e.ID || v.Revision > e.Revision {
			return invalid("environments", e.ID, "текущая версия")
		}
	}
	for _, r := range d.Resources {
		if _, ok := envs[r.EnvironmentID]; !ok {
			return invalid("resources", r.ID, "окружение")
		}
		if r.Kind != "http" && r.Kind != "grpc" && r.Kind != "postgresql" {
			return invalid("resources", r.ID, "протокол")
		}
		if err := resources.ValidateVariables(map[string]string{r.Name: ""}, nil); err != nil {
			return invalid("resources", r.ID, "имя")
		}
		if err := claim("resourceName", r.EnvironmentID+"/"+r.Name); err != nil {
			return err
		}
		rs[r.ID] = r
	}
	for _, r := range d.ResourceVersions {
		owner, ok := rs[r.ResourceID]
		if !ok {
			return invalid("resourceVersions", r.ID, "ресурс")
		}
		if !sameNames(r.SecretNames, r.Secrets) {
			return invalid("resourceVersions", r.ID, "имена секретов")
		}
		config, e := Value[resources.Config](r.Config)
		if e != nil {
			return invalid("resourceVersions", r.ID, "config")
		}
		if err := resources.ValidateVariableDefinitions(config, r.Secrets, false); err != nil {
			return invalid("resourceVersions", r.ID, "переменные")
		}
		// Historical sources may reference secrets of their frozen environment;
		// transport validation below does not fetch them or contact any endpoint.
		if config.UnavailableReason != "" {
			valid := owner.Kind == "postgresql" && config.UnavailableReason == "demo_sql_unavailable"
			found := false
			for _, b := range d.BuiltinDemo {
				found = found || b.SQLResourceID == owner.ID
			}
			if !valid || !found {
				return invalid("resourceVersions", r.ID, "системная недоступность")
			}
		} else {
			if _, e = resources.TLS(config, r.Secrets); e != nil {
				return invalid("resourceVersions", r.ID, "TLS")
			}
			if len(config.ProtoFiles) > 100 {
				return invalid("resourceVersions", r.ID, "proto bundle")
			}
			for p := range config.ProtoFiles {
				if strings.Contains(p, "..") || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\:") {
					return invalid("resourceVersions", r.ID, "proto path")
				}
			}
		}
		if err := claim("resourceRevision", fmt.Sprint(r.ResourceID, "/", r.Revision)); err != nil {
			return err
		}
		rvs[r.ID] = r
	}
	for _, r := range d.Resources {
		v, ok := rvs[r.CurrentVersionID]
		if !ok || v.ResourceID != r.ID || v.Revision != r.Revision {
			return invalid("resources", r.ID, "текущая версия")
		}
	}
	for _, i := range d.ExpectedImages {
		if _, ok := cases[i.TestCaseID]; !ok {
			return invalid("expectedImages", i.ID, "кейс")
		}
		meta, _, e := catalog.DecodeExpectedImage(catalog.ImageUpload{Filename: i.Filename, DataBase64: base64.StdEncoding.EncodeToString(i.Data)})
		if e != nil || len(i.Data) != i.SizeBytes || meta.SHA256 != i.SHA256 || meta.ContentType != i.ContentType || meta.Width != i.Width || meta.Height != i.Height {
			return invalid("expectedImages", i.ID, "байты или метаданные")
		}
		images[i.ID] = i
	}
	checkSteps := func(caseID string, steps []catalog.Step) error {
		for _, s := range steps {
			refs := []catalog.ImageRef{}
			if s.Expected != nil {
				refs = append(refs, s.Expected.Images...)
			}
			for _, b := range s.Blocks {
				refs = append(refs, b.Expected.Images...)
			}
			for _, ref := range refs {
				i, ok := images[ref.ImageID]
				if !ok || i.TestCaseID != caseID {
					return invalid("images", caseID, "принадлежность эталона")
				}
			}
		}
		return nil
	}
	for _, c := range d.TestCases {
		steps, _ := Value[[]catalog.Step](c.Steps)
		if err := checkSteps(c.ID, steps); err != nil {
			return err
		}
	}
	runSteps := map[string]map[string]bool{}
	runBlocks := map[string]map[string]bool{}
	for _, r := range d.Runs {
		if !runs.ValidHistoryPolicy(r.HistoryPolicy) {
			return invalid("runs", r.ID, "historyPolicy")
		}
		for _, reason := range r.RetentionReasons {
			if r.HistoryPolicy == "unretained" && reason != "manual" {
				return invalid("runs", r.ID, "historyPolicy/retention")
			}
		}
		if _, ok := cases[r.TestCaseID]; !ok {
			return invalid("runs", r.ID, "кейс")
		}
		ev, ok := evs[r.EnvironmentVersionID]
		if !ok {
			return invalid("runs", r.ID, "версия окружения")
		}
		c, e := Value[catalog.TestCase](r.Snapshot)
		if e != nil || c.ID != r.TestCaseID || catalog.ValidateDraft(c.Steps) != nil {
			return invalid("runs", r.ID, "снимок")
		}
		if err := checkSteps(c.ID, c.Steps); err != nil {
			return err
		}
		snap, e := Value[resources.Snapshot](r.Context)
		if e != nil || snap.EnvironmentVersionID != ev.ID || snap.EnvironmentID != ev.EnvironmentID {
			return invalid("runs", r.ID, "контекст")
		}
		for _, sr := range snap.Resources {
			rv, ok := rvs[sr.VersionID]
			if !ok || rv.ResourceID != sr.ID || sr.EnvironmentID != ev.EnvironmentID || snap.ResourceVersions[sr.Name] != rv.ID {
				return invalid("runs", r.ID, "ресурс снимка")
			}
		}
		if !oneOf(r.Status, "running", "passed", "failed", "blocked", "cancelled", "interrupted") || (r.Status == "running") != (r.FinishedAt == nil) {
			return invalid("runs", r.ID, "статус")
		}
		if !catalog.ValidID(r.IdempotencyKey) || r.RequestHash == "" {
			return invalid("runs", r.ID, "команда")
		}
		if err := claim("runKey", r.IdempotencyKey); err != nil {
			return err
		}
		if r.Status == "running" {
			if len(r.RetentionReasons) != 0 {
				return invalid("runs", r.ID, "retention")
			}
			if err := claim("activeRun", r.TestCaseID); err != nil {
				return err
			}
		}
		for _, reason := range r.RetentionReasons {
			if !oneOf(reason, "first_run", "first_success", "manual") || reason == "first_success" && r.Status != "passed" {
				return invalid("runs", r.ID, "retention")
			}
			if reason != "manual" {
				if err := claim(reason, r.TestCaseID); err != nil {
					return err
				}
			}
		}
		runSteps[r.ID] = map[string]bool{}
		runBlocks[r.ID] = map[string]bool{}
		for _, s := range c.Steps {
			runSteps[r.ID][s.ID] = true
			for _, b := range s.Blocks {
				runBlocks[r.ID][b.ID] = true
			}
		}
		rr[r.ID] = r
	}
	for _, s := range d.RunSteps {
		if !runSteps[s.RunID][s.StepID] || s.Revision < 1 || !oneOf(s.Status, "not_run", "passed", "failed", "blocked", "skipped") {
			return invalid("runSteps", s.RunID, "шаг")
		}
		if err := claim("runStep", s.RunID+"/"+s.StepID); err != nil {
			return err
		}
	}
	for id, ss := range runSteps {
		for step := range ss {
			if !unique["runStep/"+id+"/"+step] {
				return invalid("runSteps", id, "отсутствует шаг")
			}
		}
	}
	for _, s := range d.RunResourceVersions {
		run, ok := rr[s.RunID]
		rv, rvOK := rvs[s.ResourceVersionID]
		snap, _ := Value[resources.Snapshot](run.Context)
		if !ok || !rvOK || snap.ResourceVersions[s.ResourceName] != rv.ID {
			return invalid("runResourceVersions", s.RunID, "связь")
		}
		if err := claim("runResource", s.RunID+"/"+s.ResourceName); err != nil {
			return err
		}
	}
	for _, run := range d.Runs {
		snap, _ := Value[resources.Snapshot](run.Context)
		if len(snap.Resources) != len(snap.ResourceVersions) {
			return invalid("runs", run.ID, "полнота ресурсов")
		}
		for name := range snap.ResourceVersions {
			if !unique["runResource/"+run.ID+"/"+name] {
				return invalid("runResourceVersions", run.ID, "отсутствует связь")
			}
		}
	}
	for _, v := range d.Variants {
		if !runBlocks[v.RunID][v.BlockID] || v.Position < 1 || v.Position > 20 || len([]rune(v.Name)) > 200 {
			return invalid("runCallVariants", v.ID, "владелец или параметры")
		}
		if err := claim("variantPosition", fmt.Sprint(v.RunID, "/", v.BlockID, "/", v.Position)); err != nil {
			return err
		}
		vs[v.ID] = v
	}
	for _, v := range d.VariantVersions {
		p, ok := vs[v.VariantID]
		if !ok || p.RunID != v.RunID || p.BlockID != v.BlockID {
			return invalid("variantVersions", v.ID, "владелец")
		}
		req, e := Value[catalog.CallRequest](v.Request)
		if e != nil || req.ID != v.BlockID {
			return invalid("variantVersions", v.ID, "запрос")
		}
		if err := checkSteps(rr[v.RunID].TestCaseID, []catalog.Step{{Blocks: []catalog.Block{req.Block()}}}); err != nil {
			return err
		}
		if err := claim("variantRevision", fmt.Sprint(v.VariantID, "/", v.Revision)); err != nil {
			return err
		}
		vvs[v.ID] = v
	}
	for _, v := range d.Variants {
		cur, ok := vvs[v.CurrentVersionID]
		if !ok || cur.VariantID != v.ID || cur.Revision != v.Revision {
			return invalid("runCallVariants", v.ID, "версия")
		}
	}
	for _, b := range d.Batches {
		if b.StepID != "" && !runSteps[b.RunID][b.StepID] {
			return invalid("runBatches", b.ID, "шаг")
		}
		if _, ok := rr[b.RunID]; !ok || !oneOf(b.State, "running", "completed", "cancelled", "interrupted") || (b.State == "running") != (b.FinishedAt == nil) || !catalog.ValidID(b.IdempotencyKey) || b.RequestHash == "" {
			return invalid("runBatches", b.ID, "состояние")
		}
		if err := claim("batchKey", b.IdempotencyKey); err != nil {
			return err
		}
		if b.State == "running" {
			if err := claim("activeBatch", b.RunID); err != nil {
				return err
			}
		}
		batches[b.ID] = b
	}
	for _, a := range d.CallAttempts {
		if _, ok := cases[a.TestCaseID]; !ok {
			return invalid("callAttempts", a.ID, "кейс")
		}
		ev, ok := evs[a.EnvironmentVersionID]
		rv, rok := rvs[a.ResourceVersionID]
		if !ok || !rok || rs[rv.ResourceID].EnvironmentID != ev.EnvironmentID {
			return invalid("callAttempts", a.ID, "версии")
		}
		var envelope struct {
			Block    catalog.CallRequest `json:"block"`
			Resource resources.Resource  `json:"resource"`
		}
		if json.Unmarshal([]byte(a.SafeRequest), &envelope) != nil || envelope.Block.ID != a.BlockID || envelope.Resource.ID != rv.ResourceID || envelope.Resource.VersionID != rv.ID || envelope.Resource.EnvironmentID != ev.EnvironmentID {
			return invalid("callAttempts", a.ID, "снимок запроса")
		}
		if err := checkSteps(a.TestCaseID, []catalog.Step{{Blocks: []catalog.Block{envelope.Block.Block()}}}); err != nil {
			return err
		}
		if a.RunID != "" {
			run, ok := rr[a.RunID]
			if !ok || run.TestCaseID != a.TestCaseID || !runBlocks[a.RunID][a.BlockID] {
				return invalid("callAttempts", a.ID, "прогон")
			}
		}
		if a.AttemptNumber < 1 || a.DurationMS < 0 || !oneOf(a.State, "queued", "running", "completed", "error", "timed_out", "cancelled", "interrupted") || (oneOf(a.State, "queued", "running")) != (a.FinishedAt == nil) {
			return invalid("callAttempts", a.ID, "состояние")
		}
		if !catalog.ValidID(a.IdempotencyKey) || a.RequestHash == "" {
			return invalid("callAttempts", a.ID, "команда")
		}
		if err := claim("attemptKey", a.IdempotencyKey); err != nil {
			return err
		}
		if err := claim("attemptNumber", fmt.Sprint(a.RunID, "/", a.TestCaseID, "/", a.BlockID, "/", a.VariantID, "/", a.AttemptNumber)); err != nil {
			return err
		}
		if a.RunID != "" && oneOf(a.State, "running", "queued") {
			if err := claim("activeAttempt", a.RunID); err != nil {
				return err
			}
		}
		if a.VariantID != "" {
			vv, ok := vvs[a.VariantVersionID]
			if !ok || vv.VariantID != a.VariantID || vv.RunID != a.RunID || vv.BlockID != a.BlockID {
				return invalid("callAttempts", a.ID, "вариант")
			}
		} else if a.VariantVersionID != "" {
			return invalid("callAttempts", a.ID, "пара варианта")
		}
		if a.BatchID != "" {
			b, ok := batches[a.BatchID]
			if !ok || b.RunID != a.RunID || a.VariantID != "" || a.BatchPosition < 1 {
				return invalid("callAttempts", a.ID, "очередь")
			}
		} else if a.BatchPosition != 0 {
			return invalid("callAttempts", a.ID, "позиция очереди")
		}
		attempts[a.ID] = a
	}
	for _, i := range d.BatchItems {
		if batch, ok := batches[i.BatchID]; ok && batch.StepID != "" {
			doc, _ := Value[catalog.TestCase](rr[i.RunID].Snapshot)
			selected, e := runs.SelectStep(doc, batch.StepID)
			found := false
			if e == nil {
				for _, st := range selected.Steps {
					for _, bl := range st.Blocks {
						found = found || bl.ID == i.BlockID
					}
				}
			}
			if !found {
				return invalid("batchItems", i.RunID, "блок другого шага")
			}
		}
		b, ok := batches[i.BatchID]
		if !ok || b.RunID != i.RunID || !runBlocks[i.RunID][i.BlockID] || i.Position < 1 || !oneOf(i.State, "pending", "running", "completed", "cancelled", "interrupted") {
			return invalid("batchItems", i.RunID, "элемент")
		}
		if i.State == "pending" && i.AttemptID != "" {
			return invalid("batchItems", i.RunID, "pending попытка")
		}
		if i.AttemptID != "" {
			a, ok := attempts[i.AttemptID]
			if !ok || a.BatchID != i.BatchID || a.BatchPosition != i.Position || a.BlockID != i.BlockID {
				return invalid("batchItems", i.RunID, "попытка")
			}
		}
		if err := claim("batchItem", fmt.Sprint(i.BatchID, "/", i.Position)); err != nil {
			return err
		}
	}
	for _, a := range d.CallAttempts {
		if a.BatchID != "" && !unique["batchItem/"+fmt.Sprint(a.BatchID, "/", a.BatchPosition)] {
			return invalid("callAttempts", a.ID, "элемент очереди")
		}
	}
	if len(d.BuiltinDemo) > 1 {
		return invalid("builtinDemo", "", "singleton")
	}
	for _, b := range d.BuiltinDemo {
		c, cok := cases[b.TestCaseID]
		_, fok := folders[b.FolderID]
		_, eok := envs[b.EnvironmentID]
		if b.Key != "local-demo" || b.SeedVersion < 1 || !cok || !fok || !eok || c.FolderID != b.FolderID {
			return invalid("builtinDemo", "", "связи")
		}
		for kind, id := range map[string]string{"http": b.HTTPResourceID, "grpc": b.GRPCResourceID, "postgresql": b.SQLResourceID} {
			r, ok := rs[id]
			if !ok || r.EnvironmentID != b.EnvironmentID || r.Kind != kind {
				return invalid("builtinDemo", "", "ресурсы")
			}
		}
	}
	for _, i := range d.DemoItems {
		if i.Amount == "" || strings.ContainsAny(i.Amount, " \t\r\n") {
			return invalid("demoItems", "", "сумма")
		}
	}
	return validateDiagnostics(doc)
}

func sameNames(names []string, values map[string]string) bool {
	copy := slices.Clone(names)
	slices.Sort(copy)
	return slices.Equal(copy, resources.SecretNames(values))
}
func oneOf(s string, values ...string) bool { return slices.Contains(values, s) }
