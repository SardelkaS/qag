package deployment

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	appruntime "github.com/SardelkaS/qag/internal/runtime"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func paths(t *testing.T) Paths {
	t.Helper()
	root := t.TempDir()
	return Paths{Config: filepath.Join(root, "config"), PKI: filepath.Join(root, "pki"), Postgres: filepath.Join(root, "pg"), Data: filepath.Join(root, "data")}
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func TestInitPreservesIdentityAndSeparatesSecrets(t *testing.T) {
	p := paths(t)
	must(t, Init(p))
	original, err := os.ReadFile(configPath(p))
	must(t, err)
	ca, _ := os.ReadFile(filepath.Join(p.PKI, "ca.key"))
	must(t, Init(p))
	after, _ := os.ReadFile(configPath(p))
	if !bytes.Equal(original, after) {
		t.Fatal("identity rotated")
	}
	c, err := appruntime.ReadConfigFile(configPath(p))
	must(t, err)
	entries, err := os.ReadDir(p.Postgres)
	must(t, err)
	if len(entries) != 5 {
		t.Fatal("unexpected PG files")
	}
	for _, entry := range entries {
		b, _ := os.ReadFile(filepath.Join(p.Postgres, entry.Name()))
		if bytes.Contains(b, ca) || bytes.Contains(b, []byte(c.Values["QAG_SECRET_KEY"])) || entry.Name() == "ca.key" {
			t.Fatal("PostgreSQL received app or issuer secret")
		}
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(configPath(p))
		if info.Mode().Perm() != 0600 {
			t.Fatal("config not private")
		}
	}
}
func TestInitRejectsOldDataAndPartialState(t *testing.T) {
	for _, area := range []string{"data", "config", "pki", "pg"} {
		t.Run(area, func(t *testing.T) {
			p := paths(t)
			root := filepath.Dir(p.Config)
			must(t, os.MkdirAll(filepath.Join(root, area), 0700))
			must(t, os.WriteFile(filepath.Join(root, area, "sentinel"), []byte("existing"), 0600))
			if Init(p) == nil {
				t.Fatal("partial or old state accepted")
			}
			if exists(configPath(p)) {
				t.Fatal("new key generated")
			}
		})
	}
	for _, name := range []string{"ca.key", "server.key", "pg_hba.conf"} {
		t.Run(name, func(t *testing.T) {
			p := paths(t)
			must(t, Init(p))
			before, _ := os.ReadFile(configPath(p))
			must(t, os.WriteFile(filepath.Join(p.PKI, name), []byte("broken"), 0600))
			if Init(p) == nil {
				t.Fatal("broken PKI accepted")
			}
			after, _ := os.ReadFile(configPath(p))
			if !bytes.Equal(before, after) {
				t.Fatal("key replaced")
			}
		})
	}
	p := paths(t)
	must(t, Init(p))
	must(t, os.Remove(filepath.Join(p.Config, "complete")))
	if Init(p) == nil {
		t.Fatal("unfinished state repaired silently")
	}
}
func TestImportExportRestoreAndConflict(t *testing.T) {
	original := paths(t)
	must(t, Init(original))
	source := filepath.Join(t.TempDir(), "native")
	must(t, Export(original, source))
	envBefore, _ := os.ReadFile(filepath.Join(source, "dev.env"))
	caBefore, _ := os.ReadFile(filepath.Join(source, "pki", "ca.key"))
	target := paths(t)
	must(t, os.MkdirAll(target.Data, 0700))
	must(t, os.WriteFile(filepath.Join(target.Data, "PG_VERSION"), []byte("18"), 0600))
	must(t, ImportLocal(target, source))
	must(t, ImportLocal(target, source))
	a, _ := appruntime.ReadConfigFile(configPath(original))
	b, _ := appruntime.ReadConfigFile(configPath(target))
	if !reflect.DeepEqual(a, b) {
		t.Fatal("original credentials or demo role changed")
	}
	after, _ := os.ReadFile(filepath.Join(source, "dev.env"))
	caAfter, _ := os.ReadFile(filepath.Join(source, "pki", "ca.key"))
	if !bytes.Equal(envBefore, after) || !bytes.Equal(caBefore, caAfter) {
		t.Fatal("source modified")
	}
	if Export(original, source) == nil {
		t.Fatal("export overwrote source")
	}
	backup := filepath.Join(t.TempDir(), "backup")
	must(t, Export(target, backup))
	restored := paths(t)
	must(t, Restore(restored, backup))
	c, _ := appruntime.ReadConfigFile(configPath(restored))
	if !reflect.DeepEqual(a, c) {
		t.Fatal("restore lost key")
	}
	conflict := paths(t)
	must(t, Init(conflict))
	current, _ := os.ReadFile(configPath(conflict))
	if ImportLocal(conflict, source) == nil {
		t.Fatal("conflict accepted")
	}
	currentAfter, _ := os.ReadFile(configPath(conflict))
	if !bytes.Equal(current, currentAfter) {
		t.Fatal("conflict wrote data")
	}
	for _, replacement := range []struct{ from, to string }{{"sslmode=disable", "sslmode=require"}, {"127.0.0.1:15432", "remote.invalid:15432"}} {
		t.Run(replacement.to, func(t *testing.T) {
			must(t, os.WriteFile(filepath.Join(source, "dev.env"), bytes.ReplaceAll(envBefore, []byte(replacement.from), []byte(replacement.to)), 0600))
			p := paths(t)
			if ImportLocal(p, source) == nil {
				t.Fatal("unsupported import accepted")
			}
			if exists(configPath(p)) {
				t.Fatal("rejected import wrote state")
			}
		})
	}
}
func TestPrivateConfigErrorsDoNotPrintInput(t *testing.T) {
	p := paths(t)
	must(t, Init(p))
	c, _ := appruntime.ReadConfigFile(configPath(p))
	c.Values["QAG_SECRET_KEY"] = "sentinel-private"
	b, _ := json.Marshal(c)
	must(t, os.WriteFile(configPath(p), b, 0600))
	err := Check(p)
	if err == nil || strings.Contains(err.Error(), "sentinel-private") {
		t.Fatal("invalid key accepted or disclosed")
	}
}
