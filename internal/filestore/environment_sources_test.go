package filestore_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/filestore"
	"github.com/SardelkaS/qag/internal/resources"
)

func TestEnvironmentSourcesAndFullResourceSurviveRestart(t *testing.T) {
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "state.yaml")
	s, err := filestore.Open(p, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	r := &filestore.Resources{Store: s}
	env, err := r.SaveEnvironmentMetadata(ctx, "", "Auth", "before", 0)
	if err != nil {
		t.Fatal(err)
	}
	source := catalog.HTTPHeaderSource{Method: "POST", URL: "http://127.0.0.1:1/token", TLS: catalog.TLSConfig{Mode: "plaintext"}, BodyMode: "form", Form: []catalog.KeyValue{{Name: "secret", Value: "${AUTH_SECRET}"}, {Name: "scope", Value: "one"}, {Name: "scope", Value: "two"}}, JSONPointer: "/token", Prefix: "[", Suffix: "]", FetchMode: "per_run"}
	env, err = r.SaveVariables(ctx, env.ID, env.Revision, map[string]string{"STATIC": "literal"}, resources.SecretChanges{Replace: map[string]string{"AUTH_SECRET": "environment-secret"}}, map[string]catalog.HTTPHeaderSource{"AUTH": source, " ": {}})
	if err != nil || len(env.HTTPVariables) != 1 {
		t.Fatal("save HTTP definitions", err)
	}
	config := resources.Config{BaseURL: "http://127.0.0.1:1/api", TLS: catalog.TLSConfig{Mode: "plaintext"}, Headers: []catalog.RequestHeader{{Name: "Authorization", Value: "Bearer ${AUTH}"}, {ID: catalog.NewID(), Name: "X-Direct", Source: &source}}, Variables: map[string]resources.VariableDefinition{"LOCAL": {Mode: "literal", Value: "local"}, "OWN": {Mode: "http", Source: &source}}}
	resource, err := r.SaveResource(ctx, "", env.ID, "TARGET", "http", 0, config, resources.SecretChanges{Replace: map[string]string{"LOCAL_SECRET": "local-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := r.Freeze(ctx, env.ID, map[string]string{"TARGET": "http"})
	if err != nil {
		t.Fatal(err)
	}
	env, err = r.SaveEnvironmentMetadata(ctx, env.ID, env.Name, "after", env.Revision)
	if err != nil || env.HTTPVariables["AUTH"].Suffix != "]" || env.Variables["STATIC"] != "literal" {
		t.Fatal("metadata erased config", err)
	}
	env, err = r.SaveVariables(ctx, env.ID, env.Revision, env.Variables, resources.SecretChanges{})
	if err != nil || len(env.HTTPVariables) != 1 {
		t.Fatal("old client erased sources", err)
	}
	if _, err = r.SaveVariables(ctx, env.ID, env.Revision, env.Variables, resources.SecretChanges{}, map[string]catalog.HTTPHeaderSource{"LOCAL": source}); err == nil {
		t.Fatal("local/environment collision accepted")
	}
	unchanged, _ := r.GetEnvironment(ctx, env.ID)
	if unchanged.Revision != env.Revision {
		t.Fatal("conflict changed revision")
	}
	resolved, err := r.Resolve(ctx, frozen, "TARGET")
	if err != nil || resolved.EnvironmentSecrets["AUTH_SECRET"] != "environment-secret" || resolved.EnvironmentHTTPVariables["AUTH"].Suffix != "]" {
		t.Fatal("frozen source lost", err)
	}
	bad := config
	bad.Headers = append([]catalog.RequestHeader{}, config.Headers...)
	bad.Headers[1].Source = &catalog.HTTPHeaderSource{Method: "POST", URL: "http://127.0.0.1:1/token", BodyMode: "form", JSONPointer: "bad~pointer", FetchMode: "per_run", TLS: catalog.TLSConfig{Mode: "plaintext"}}
	if _, err = r.SaveResource(ctx, resource.ID, env.ID, resource.Name, resource.Kind, resource.Revision, bad, resources.SecretChanges{Clear: []string{"LOCAL_SECRET"}}); err == nil {
		t.Fatal("invalid nested section accepted")
	}
	actual, _ := r.GetResource(ctx, resource.ID)
	if actual.Revision != resource.Revision || !reflect.DeepEqual(actual.Config, resource.Config) {
		t.Fatal("partial resource write")
	}
	s.Failpoint = func(stage string) error {
		if stage == "before-replace" {
			return errors.New("injected disk failure")
		}
		return nil
	}
	if _, err = r.SaveResource(ctx, resource.ID, env.ID, "CHANGED", resource.Kind, resource.Revision, config, resources.SecretChanges{Clear: []string{"LOCAL_SECRET"}}); err == nil {
		t.Fatal("failed resource save succeeded")
	}
	s.Failpoint = nil
	actual, _ = r.GetResource(ctx, resource.ID)
	if actual.Revision != resource.Revision || actual.Name != resource.Name {
		t.Fatal("failed disk write published")
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), "version: 3") || !strings.Contains(string(data), "jsonPointer:") {
		t.Fatal("missing v3 or source fields")
	}
	s.Close()
	s, err = filestore.Open(p, false)
	if err != nil {
		t.Fatal(err)
	}
	r = &filestore.Resources{Store: s}
	actual, err = r.GetResource(ctx, resource.ID)
	if err != nil || !reflect.DeepEqual(actual.Config, resource.Config) || len(actual.SecretNames) != 1 {
		t.Fatal("full resource lost after restart", err)
	}
	current, err := r.GetEnvironment(ctx, env.ID)
	if err != nil || len(current.HTTPVariables) != 1 || current.Description != "after" {
		t.Fatal("environment lost after restart", err)
	}
	current, err = r.SaveVariables(ctx, current.ID, current.Revision, current.Variables, resources.SecretChanges{}, map[string]catalog.HTTPHeaderSource{})
	if err != nil || len(current.HTTPVariables) != 0 {
		t.Fatal("explicit clear failed", err)
	}
}
