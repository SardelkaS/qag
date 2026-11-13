//go:build integration

package datatransfer_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/dataset"
	"github.com/SardelkaS/qag/internal/datatransfer"
	"github.com/SardelkaS/qag/internal/demo"
	"github.com/SardelkaS/qag/internal/filestore"
	"github.com/SardelkaS/qag/internal/resources"
	"github.com/SardelkaS/qag/internal/runs"
	"github.com/SardelkaS/qag/internal/service"
	"github.com/SardelkaS/qag/internal/storage"
	"github.com/SardelkaS/qag/internal/testfixture"
	"google.golang.org/grpc"
	"gopkg.in/yaml.v3"
)

func TestFullGraphCrossKeyRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	file := filepath.Join(t.TempDir(), "source.yaml")
	s, err := filestore.Open(file, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Bootstrap(ctx, "http://127.0.0.1:18088", "127.0.0.1:19091"); err != nil {
		t.Fatal(err)
	}
	c, res := &filestore.Catalog{Store: s}, &filestore.Resources{Store: s}
	folder, err := c.CreateFolder(ctx, "Свои проверки")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Ёж", "Еж", "Ель", "Alpha", "Бета", "Яблоко", "A2", "1 тест", "_тест", "AБ", "AA", "АБ", "АA", "α", "ёжик", "Ёлка"} {
		if _, err = c.CreateFolder(ctx, name); err != nil {
			t.Fatal(err)
		}
	}
	tc, err := c.CreateCase(ctx, folder.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	env, err := res.SaveEnvironment(ctx, "", "Секреты", 0, map[string]string{"N": "9007199254740993123.125"}, resources.SecretChanges{Replace: map[string]string{"TOKEN": "private-transfer-sentinel"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = res.SaveEnvironment(ctx, env.ID, env.Name, env.Revision, env.Variables, resources.SecretChanges{Replace: map[string]string{"TOKEN": "new-private-sentinel"}})
	if err != nil {
		t.Fatal(err)
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"n":9007199254740993123.125,"token":"new-private-sentinel"}`))
	}))
	defer target.Close()
	env, err = res.GetEnvironment(ctx, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	envSource := catalog.HTTPHeaderSource{Method: "POST", URL: target.URL, TLS: catalog.TLSConfig{Mode: "plaintext"}, BodyMode: "form", Form: []catalog.KeyValue{{Name: "secret", Value: "${TOKEN}"}, {Name: "scope", Value: "one"}, {Name: "scope", Value: "two"}}, JSONPointer: "/token", Suffix: "]", FetchMode: "per_run"}
	env, err = res.SaveVariables(ctx, env.ID, env.Revision, env.Variables, resources.SecretChanges{}, map[string]catalog.HTTPHeaderSource{"ENV_HTTP": envSource})
	if err != nil {
		t.Fatal(err)
	}
	resource, err := res.SaveResource(ctx, "", env.ID, "TARGET", "http", 0, resources.Config{BaseURL: target.URL, Variables: map[string]resources.VariableDefinition{"LOCAL": {Mode: "literal", Value: "${N}"}, "FROM_HTTP": {Mode: "http", Source: &catalog.HTTPHeaderSource{Method: "GET", URL: target.URL, BodyMode: "none", TLS: catalog.TLSConfig{Mode: "plaintext"}, JSONPointer: "/token", FetchMode: "per_run"}}}, Headers: []catalog.RequestHeader{{Name: "Authorization", Value: "${TOKEN}"}}, TLS: resources.TLSConfig{Mode: "plaintext"}}, resources.SecretChanges{})
	if err != nil {
		t.Fatal(err)
	}
	var picture bytes.Buffer
	if err = png.Encode(&picture, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	meta, err := c.UploadImage(ctx, tc.ID, catalog.ImageUpload{Filename: "Эталон.png", DataBase64: base64.StdEncoding.EncodeToString(picture.Bytes())})
	if err != nil {
		t.Fatal(err)
	}
	code := 200
	step := catalog.Step{ID: catalog.NewID(), Description: "Точный ответ", Expected: &catalog.StepExpectation{Text: "Изображение", Images: []catalog.ImageRef{{ImageID: meta.ID, Caption: "Старый эталон"}}}, Blocks: []catalog.Block{{ID: catalog.NewID(), Kind: "http", Resource: "TARGET", HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/", BodyMode: "none"}, Expected: catalog.Expectation{StatusCode: &code, BodyMode: "none"}}}}
	tc, err = c.SaveCase(ctx, tc.ID, tc.Name, tc.Revision, []catalog.Step{step})
	if err != nil {
		t.Fatal(err)
	}
	rr := &filestore.Runs{Store: s}
	engine := service.New(ctx, c, res, rr)
	defer engine.Close()
	run, err := engine.StartRunWithPolicy(ctx, tc.ID, env.ID, tc.Revision, catalog.NewID(), "unretained", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = engine.StartStep(ctx, run.ID, step.ID, catalog.NewID()); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 100; n++ {
		run, err = engine.GetRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if run.Batches[0].State == "completed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(run.Attempts) != 1 || run.Attempts[0].Result == nil || !run.Attempts[0].Result.Redacted {
		t.Fatal("source attempt")
	}
	variant, err := rr.SaveVariant(ctx, run.ID, step.Blocks[0].ID, "", "Вариант", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := variant.Versions[0].Request
	request.TimeoutSeconds = 15
	variant, err = rr.SaveVariant(ctx, run.ID, "", variant.ID, variant.Name, variant.Revision, &request)
	if err != nil {
		t.Fatal(err)
	}
	a, err := engine.VariantAttempt(ctx, run.ID, variant.ID, variant.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 100; n++ {
		a, err = engine.GetAttempt(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if a.State == "completed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = rr.PassAll(ctx, run.ID, []runs.StepRevision{{StepID: step.ID, Revision: 1}}); err != nil {
		t.Fatal(err)
	}
	run, err = engine.FinishRun(ctx, run.ID)
	if err != nil || run.Status != "passed" {
		t.Fatal("source finish", err)
	}
	engine.Close()
	resource.Config.Headers = []catalog.RequestHeader{}
	if _, err = res.SaveResource(ctx, resource.ID, env.ID, resource.Name, resource.Kind, resource.Revision, resource.Config, resources.SecretChanges{}); err != nil {
		t.Fatal(err)
	}
	tc.Steps[0].Expected = nil
	tc, err = c.SaveCase(ctx, tc.ID, tc.Name, tc.Revision, tc.Steps)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := res.Freeze(ctx, env.ID, map[string]string{"TARGET": "http"})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := rr.Start(ctx, tc, frozen, catalog.NewID(), "pending-command", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = rr.BeginAttempt(ctx, catalog.NewID(), tc.ID, pending.ID, tc.Revision, tc.Steps[0].Blocks[0], frozen, tc.Steps[0].Blocks[0], catalog.NewID(), "pending-attempt", runs.AttemptOptions{BatchID: pending.Batches[0].ID, BatchPosition: 1}); err != nil {
		t.Fatal(err)
	}
	if err = s.Update(ctx, func(d *dataset.Data) error {
		for i := range d.RunSteps {
			if d.RunSteps[i].RunID == run.ID {
				d.RunSteps[i].Status = "skipped"
			}
		}
		req, e := dataset.Value[catalog.CallRequest](d.VariantVersions[0].Request)
		if e != nil {
			return e
		}
		req.Expected = &catalog.Expectation{BodyMode: "text", Body: "legacy expected"}
		d.VariantVersions[0].Request = dataset.Raw(req)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if i == 2 {
			// Reconstruct a real v1 document, including history, then exercise the
			// supported v1 reader before importing it into a third PostgreSQL key.
			doc.Version = 1
			for j := range doc.Data.EnvironmentVersions {
				doc.Data.EnvironmentVersions[j].HTTPVariables = nil
			}
			for j := range doc.Data.Runs {
				doc.Data.Runs[j].HistoryPolicy = ""
				context, _ := dataset.Value[resources.Snapshot](doc.Data.Runs[j].Context)
				for k := range context.Resources {
					context.Resources[k].Config.Variables = nil
				}
				doc.Data.Runs[j].Context = dataset.Raw(context)
			}
			for j := range doc.Data.Batches {
				doc.Data.Batches[j].StepID = ""
			}
			for j := range doc.Data.ResourceVersions {
				config, _ := dataset.Value[resources.Config](doc.Data.ResourceVersions[j].Config)
				config.Variables = nil
				doc.Data.ResourceVersions[j].Config = dataset.Raw(config)
			}
			legacy, err := yaml.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			doc, err = dataset.Decode(bytes.NewReader(legacy))
			if err != nil || doc.Version != dataset.Version {
				t.Fatal("v1-to-v2", err)
			}
		}
		f := testfixture.ForTest(t, ctx)
		store, err := storage.Open(ctx, f.AppURL)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		if err = store.AcquireInstance(ctx); err != nil {
			t.Fatal(err)
		}
		vault, _ := resources.NewVault(bytes.Repeat([]byte{byte(i + 1)}, 32))
		pg := &datatransfer.PostgreSQL{Store: store, Vault: vault}
		if _, err = pg.Import(ctx, doc, true); err != nil {
			t.Fatal("dry run", err)
		}
		ready, err := store.CheckSchema(ctx)
		if err != nil || ready {
			t.Fatal("dry run migrated target", err)
		}
		if _, err = pg.Import(ctx, doc, false); err != nil {
			t.Fatal("import", err)
		}
		snapshot, err := pg.Snapshot(ctx)
		if err != nil {
			t.Fatal("snapshot", err)
		}
		if _, counts, err := dataset.Merge(snapshot, doc); err != nil {
			t.Fatal("round trip equality", counts, err)
		}
		if i != 2 {
			found := false
			for _, version := range snapshot.Data.EnvironmentVersions {
				if source, ok := version.HTTPVariables["ENV_HTTP"]; ok {
					found = true
					if source.Suffix != "]" || source.BodyMode != "form" || len(source.Form) != 3 {
						t.Fatal("HTTP environment transfer lost fields")
					}
				}
			}
			if !found {
				t.Fatal("HTTP environment transfer lost definition")
			}
		}
		if !bytes.Equal(snapshot.Data.ExpectedImages[0].Data, picture.Bytes()) || len(snapshot.Data.CallAttempts) != 3 || len(snapshot.Data.VariantVersions) != 2 {
			t.Fatal("full graph was truncated")
		}
		pgRuns := runs.NewRepository(store.Pool)
		fileFolders, err := c.ListFolders(ctx, false, 100, "")
		if err != nil {
			t.Fatal(err)
		}
		pgFolders, err := catalog.NewRepository(store.Pool).ListFolders(ctx, false, 100, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(fileFolders.Items) != len(pgFolders.Items) {
			t.Fatal("catalog count")
		}
		for j := range fileFolders.Items {
			if fileFolders.Items[j].Name != pgFolders.Items[j].Name {
				t.Fatalf("alphabet mismatch at %d: YAML=%q PostgreSQL=%q", j, fileFolders.Items[j].Name, pgFolders.Items[j].Name)
			}
		}
		report, err := pgRuns.ReportSnapshot(ctx, run.ID, true)
		if err != nil || len(report.Images) != 1 || report.Images[0].SHA256 != meta.SHA256 {
			t.Fatal("historical image", err)
		}
		oldRun, err := pgRuns.Get(ctx, run.ID)
		policy, selected, variableCount := "unretained", step.ID, 2
		if i == 2 {
			policy, selected, variableCount = "auto", "", 0
		}
		if err != nil || oldRun.HistoryPolicy != policy || oldRun.Batches[0].StepID != selected || len(oldRun.RetentionReasons) != 0 || len(oldRun.Resources[0].Config.Variables) != variableCount || oldRun.StepStates[0].LegacyStatus != "skipped" || oldRun.Variants[0].Versions[0].Request.Expected != nil {
			t.Fatal("legacy projection", err)
		}
		if _, err = pg.Import(ctx, doc, false); err != nil {
			t.Fatal("repeat import", err)
		}
		for _, changed := range []string{"policy", "step", "environment-source"} {
			if i == 2 {
				continue
			}
			conflict, _ := dataset.Clone(doc)
			if changed == "policy" {
				for j := range conflict.Data.Runs {
					if conflict.Data.Runs[j].ID == run.ID {
						conflict.Data.Runs[j].HistoryPolicy = "auto"
					}
				}
			} else if changed == "step" {
				for j := range conflict.Data.Batches {
					if conflict.Data.Batches[j].RunID == run.ID {
						conflict.Data.Batches[j].StepID = ""
					}
				}
			} else {
				for j := range conflict.Data.EnvironmentVersions {
					if source, ok := conflict.Data.EnvironmentVersions[j].HTTPVariables["ENV_HTTP"]; ok {
						source.Suffix = "different"
						conflict.Data.EnvironmentVersions[j].HTTPVariables["ENV_HTTP"] = source
					}
				}
			}
			if _, err = pg.Import(ctx, conflict, false); err == nil {
				t.Fatal("different " + changed + " accepted")
			}
		}
		copy, _ := dataset.Clone(doc)
		copy.Data.TestCases[len(copy.Data.TestCases)-1].Revision++
		if _, err = pg.Import(ctx, copy, false); err == nil {
			t.Fatal("conflict accepted")
		}
		unchanged, err := pg.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = dataset.Merge(snapshot, unchanged); err != nil {
			t.Fatal("partial conflict write", err)
		}
		encoded, err := dataset.Encode(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		doc, err = dataset.Decode(bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			if err = pgRuns.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			restored, err := pgRuns.Get(ctx, pending.ID)
			if err != nil || restored.Status != "interrupted" || restored.Attempts[0].State != "interrupted" {
				t.Fatal("recovery", err)
			}
			demoConfig, demoSecrets, err := demo.DatabaseConfig(f.AppURL, f.ReaderURL)
			if err != nil {
				t.Fatal(err)
			}
			reader, err := demo.OpenReader(ctx, store.Pool, demoConfig, demoSecrets)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			demoService := &demo.Service{Pool: reader}
			httpDemo := httptest.NewServer(demoService.Handler())
			defer httpDemo.Close()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			grpcServer := grpc.NewServer()
			demoService.RegisterGRPC(grpcServer)
			go grpcServer.Serve(listener)
			defer grpcServer.Stop()
			info, err := demo.Bootstrap(ctx, store.Pool, vault, httpDemo.URL, listener.Addr().String(), demoConfig, demoSecrets)
			if err != nil {
				t.Fatal(err)
			}
			pgCatalog := catalog.NewRepository(store.Pool)
			demoCase, err := pgCatalog.GetCase(ctx, info.TestCaseID)
			if err != nil {
				t.Fatal(err)
			}
			pgEngine := service.New(ctx, pgCatalog, resources.NewRepository(store.Pool, vault), pgRuns)
			defer pgEngine.Close()
			demoRun, err := pgEngine.StartRun(ctx, demoCase.ID, info.EnvironmentID, demoCase.Revision, catalog.NewID(), true)
			if err != nil {
				t.Fatal(err)
			}
			for n := 0; n < 100; n++ {
				demoRun, err = pgEngine.GetRun(ctx, demoRun.ID)
				if err != nil {
					t.Fatal(err)
				}
				if demoRun.Batches[0].State == "completed" {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if len(demoRun.Attempts) != 5 {
				t.Fatal("rebound demo incomplete")
			}
			for _, a := range demoRun.Attempts {
				if a.State != "completed" || a.Result == nil || a.Result.Comparison.Status != "match" {
					t.Fatalf("rebound demo %s: %s %+v", a.Resource.Kind, a.State, a.Result)
				}
			}
		}
	}
}
