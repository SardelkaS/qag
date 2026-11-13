//go:build integration

package resources_test

import (
	"bytes"
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/resources"
	"github.com/SardelkaS/qag/internal/storage"
	"github.com/SardelkaS/qag/internal/testfixture"
)

func TestEnvironmentHTTPDefinitionsAndFullResourceVersions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	f := testfixture.ForTest(t, ctx)
	s, err := storage.Open(ctx, f.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	vault, _ := resources.NewVault(bytes.Repeat([]byte{11}, 32))
	r := resources.NewRepository(s.Pool, vault)
	env, err := r.SaveEnvironmentMetadata(ctx, "", "Auth", "before", 0)
	if err != nil {
		t.Fatal(err)
	}
	source := catalog.HTTPHeaderSource{Method: "POST", URL: "http://127.0.0.1:1/token", BodyMode: "form", Form: []catalog.KeyValue{{Name: "secret", Value: "${AUTH_SECRET}"}}, TLS: catalog.TLSConfig{Mode: "plaintext"}, JSONPointer: "/token", Prefix: "[", Suffix: "]", FetchMode: "per_run"}
	env, err = r.SaveVariables(ctx, env.ID, env.Revision, map[string]string{"STATIC": "before"}, resources.SecretChanges{Replace: map[string]string{"AUTH_SECRET": "before-secret"}}, map[string]catalog.HTTPHeaderSource{"AUTH": source})
	if err != nil {
		t.Fatal(err)
	}
	cfg := resources.Config{BaseURL: "http://127.0.0.1:1/api", TLS: catalog.TLSConfig{Mode: "plaintext"}, Headers: []catalog.RequestHeader{{Name: "Authorization", Value: "Bearer ${AUTH}"}, {ID: catalog.NewID(), Name: "X-Direct", Source: &source}}, Variables: map[string]resources.VariableDefinition{"LOCAL": {Mode: "literal", Value: "before"}}}
	res, err := r.SaveResource(ctx, "", env.ID, "TARGET", "http", 0, cfg, resources.SecretChanges{Replace: map[string]string{"PRIVATE": "before-private"}})
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := r.Freeze(ctx, env.ID, map[string]string{"TARGET": "http"})
	if err != nil {
		t.Fatal(err)
	}
	env, err = r.SaveEnvironmentMetadata(ctx, env.ID, env.Name, "after", env.Revision)
	if err != nil || len(env.HTTPVariables) != 1 {
		t.Fatal("metadata lost sources", err)
	}
	source.Suffix = "new"
	env, err = r.SaveVariables(ctx, env.ID, env.Revision, map[string]string{"STATIC": "after"}, resources.SecretChanges{Replace: map[string]string{"AUTH_SECRET": "after-secret"}}, map[string]catalog.HTTPHeaderSource{"AUTH": source})
	if err != nil {
		t.Fatal(err)
	}
	old, err := r.Resolve(ctx, frozen, "TARGET")
	if err != nil || old.EnvironmentHTTPVariables["AUTH"].Suffix != "]" || old.EnvironmentSecrets["AUTH_SECRET"] != "before-secret" || old.Variables["STATIC"] != "before" {
		t.Fatal("old context changed", err)
	}
	if _, err = r.SaveVariables(ctx, env.ID, env.Revision, env.Variables, resources.SecretChanges{}, map[string]catalog.HTTPHeaderSource{"LOCAL": source}); err == nil {
		t.Fatal("collision accepted")
	}
	unchanged, _ := r.GetEnvironment(ctx, env.ID)
	if unchanged.Revision != env.Revision {
		t.Fatal("partial environment update")
	}
	env, err = r.SaveVariables(ctx, env.ID, env.Revision, env.Variables, resources.SecretChanges{})
	if err != nil || len(env.HTTPVariables) != 1 {
		t.Fatal("legacy write erased source", err)
	}
	cfg.BaseURL = "http://127.0.0.1:2/new"
	cfg.Variables["LOCAL"] = resources.VariableDefinition{Mode: "literal", Value: "after"}
	updated, err := r.SaveResource(ctx, res.ID, env.ID, res.Name, res.Kind, res.Revision, cfg, resources.SecretChanges{Replace: map[string]string{"PRIVATE": "after-private"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.SaveResource(ctx, res.ID, env.ID, "STALE", res.Kind, res.Revision, cfg, resources.SecretChanges{Clear: []string{"PRIVATE"}}); err == nil {
		t.Fatal("stale write accepted")
	}
	bad := cfg
	bad.TLS = resources.TLSConfig{Mode: "mtls", Certificate: "invalid"}
	if _, err = r.SaveResource(ctx, res.ID, env.ID, "INVALID", res.Kind, updated.Revision, bad, resources.SecretChanges{Clear: []string{"PRIVATE"}}); err == nil {
		t.Fatal("invalid nested write accepted")
	}
	actual, _ := r.GetResource(ctx, res.ID)
	if actual.Name != updated.Name || actual.Revision != updated.Revision || !reflect.DeepEqual(actual.Config, updated.Config) {
		t.Fatal("partial resource update")
	}
	snapshot, _ := r.Freeze(ctx, env.ID, map[string]string{"TARGET": "http"})
	current, err := r.Resolve(ctx, snapshot, "TARGET")
	if err != nil || current.Secrets["PRIVATE"] != "after-private" || current.EnvironmentHTTPVariables["AUTH"].Suffix != "new" {
		t.Fatal("updated full context", err)
	}
	env, err = r.SaveVariables(ctx, env.ID, env.Revision, env.Variables, resources.SecretChanges{}, map[string]catalog.HTTPHeaderSource{})
	if err != nil || len(env.HTTPVariables) != 0 {
		t.Fatal("explicit clear", err)
	}
}
