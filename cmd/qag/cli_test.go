package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SardelkaS/qag/internal/filestore"
)

func TestCLINoSideEffectsAndSelectorConflicts(t *testing.T) {
	file := filepath.Join(t.TempDir(), "data.yaml")
	env := func(k string) (string, bool) {
		if k == "QAG_DATABASE_URL" {
			return "postgres://u:sentinel@unreachable/qag", true
		}
		return "", false
	}
	for _, args := range [][]string{{"--help"}, {"--version"}, {"data", "import", "--help"}} {
		var out bytes.Buffer
		if err := command(args, env, &out); err != nil {
			t.Fatal(args, err)
		}
		if strings.Contains(out.String(), "sentinel") {
			t.Fatal("secret leaked")
		}
	}
	for _, args := range [][]string{{"--unknown=sentinel"}, {"unknown"}, {"serve", "extra"}, {"serve", "--data-file", file, "--database-url", "postgres://u:sentinel@localhost/qag"}, {"serve", "--storage", "postgres", "--data-file", file}, {"data", "import", "--data-file", file}} {
		var out bytes.Buffer
		err := command(args, env, &out)
		var ce *commandError
		if !errors.As(err, &ce) || ce.code != 2 {
			t.Fatal(args, err)
		}
		if strings.Contains(err.Error(), "sentinel") {
			t.Fatal("secret leaked")
		}
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("syntax touched store")
	}
}

func TestCLIFileTransferDryRunAndBusy(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.yaml")
	output := filepath.Join(dir, "backup.yaml")
	target := filepath.Join(dir, "target.yaml")
	s, err := filestore.Open(source, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Bootstrap(context.Background(), "http://127.0.0.1:18088", "127.0.0.1:19091"); err != nil {
		t.Fatal(err)
	}
	env := func(string) (string, bool) { return "", false }
	var out bytes.Buffer
	if err = command([]string{"data", "export", "--data-file", source, "--output", output}, env, &out); err == nil {
		t.Fatal("export of busy store succeeded")
	}
	s.Close()
	if err = command([]string{"data", "export", "--data-file", source, "--output", output}, env, &out); err != nil {
		t.Fatal(err)
	}
	if err = command([]string{"data", "export", "--data-file", source, "--output", output}, env, &out); err == nil {
		t.Fatal("output overwritten")
	}
	args := []string{"data", "import", "--data-file", target, "--input", output}
	if err = command(append(args, "--dry-run"), env, &out); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("dry run created target")
	}
	if err = command(args, env, &out); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(target)
	bytesBefore, _ := os.ReadFile(target)
	if err = command(args, env, &out); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(target)
	bytesAfter, _ := os.ReadFile(target)
	if !before.ModTime().Equal(after.ModTime()) || !bytes.Equal(bytesBefore, bytesAfter) {
		t.Fatal("repeat import wrote existing data")
	}
	if err = command([]string{"data", "import", "--data-file", target, "--input", target}, env, &out); err == nil {
		t.Fatal("input=target accepted")
	}
}
