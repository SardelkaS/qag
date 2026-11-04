package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"runtime/debug"
	"time"

	"github.com/SardelkaS/qag/internal/dataset"
	"github.com/SardelkaS/qag/internal/datatransfer"
	"github.com/SardelkaS/qag/internal/filestore"
	"github.com/SardelkaS/qag/internal/resources"
	appruntime "github.com/SardelkaS/qag/internal/runtime"
	"github.com/SardelkaS/qag/internal/storage"
)

const usage = `qag — ручные проверки через Web UI и MCP

qag [serve] [параметры]          запустить сервер (по умолчанию ./qag.yaml)
qag data export --output FILE   экспортировать полные данные в новый YAML
qag data import --input FILE    объединить YAML с выбранным хранилищем
qag healthcheck [--address HOST:PORT]
qag --help | --version

Параметры хранилища:
  --storage yaml|postgres       выбрать backend явно
  --data-file FILE              рабочий YAML-файл
  --database-url DSN            PostgreSQL приложения (или QAG_DATABASE_URL)
  --demo-database-url DSN       отдельная demo-роль, только serve/PostgreSQL
  --secret-key BASE64           ключ PostgreSQL (или QAG_SECRET_KEY)
  --config FILE                 приватный JSON установки
  --address HOST:PORT            HTTP-listener, default 127.0.0.1:8080
  --dry-run                     проверить import без записи

Флаги переопределяют env, env переопределяет config. QAG_STORAGE и QAG_DATA_FILE
также поддерживаются. Старый PostgreSQL-config продолжает выбирать PostgreSQL.
YAML содержит открытые секреты и приватные ключи; храните его с приватными правами.
Перед переносом остановите qag, использующий это хранилище. Импорт добавляет новые
ID и пропускает одинаковые; любой конфликт отклоняет весь импорт без перезаписи.

Примеры:
  qag serve --data-file ./workspace.yaml
  qag data export --storage postgres --output ./backup.yaml
  qag data import --data-file ./other.yaml --input ./backup.yaml --dry-run
`

type commandError struct {
	code int
	err  error
}

func (e *commandError) Error() string { return e.err.Error() }
func (e *commandError) Unwrap() error { return e.err }
func bad(message string) error        { return &commandError{2, errors.New(message)} }

func command(args []string, env func(string) (string, bool), out io.Writer) error {
	cmd := "serve"
	rest := args
	if len(rest) > 0 && !isFlag(rest[0]) {
		cmd = rest[0]
		rest = rest[1:]
		if cmd == "data" {
			if len(rest) == 0 {
				return bad("укажите data export или data import; см. qag --help")
			}
			cmd += " " + rest[0]
			rest = rest[1:]
		}
	}
	if cmd != "serve" && cmd != "data export" && cmd != "data import" && cmd != "healthcheck" {
		return bad("неизвестная команда; см. qag --help")
	}
	fs := flag.NewFlagSet("qag", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	help := fs.Bool("help", false, "")
	fs.BoolVar(help, "h", false, "")
	version := fs.Bool("version", false, "")
	values := map[string]*string{}
	keys := map[string]string{"storage": "QAG_STORAGE", "data-file": "QAG_DATA_FILE", "database-url": "QAG_DATABASE_URL", "demo-database-url": "QAG_DEMO_DATABASE_URL", "secret-key": "QAG_SECRET_KEY", "address": "QAG_ADDRESS", "config": "QAG_CONFIG_FILE"}
	for name := range keys {
		values[name] = fs.String(name, "", "")
	}
	input := fs.String("input", "", "")
	output := fs.String("output", "", "")
	dry := fs.Bool("dry-run", false, "")
	if e := fs.Parse(rest); e != nil {
		return bad("некорректные параметры; см. qag --help")
	}
	if fs.NArg() != 0 {
		return bad("лишние аргументы; см. qag --help")
	}
	if *help {
		_, e := fmt.Fprint(out, usage)
		return e
	}
	if *version {
		v := "dev"
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = info.Main.Version
		}
		_, e := fmt.Fprintln(out, "qag", v)
		return e
	}
	explicit := map[string]bool{}
	overrides := map[string]string{}
	fs.Visit(func(f *flag.Flag) {
		explicit[f.Name] = true
		if key, ok := keys[f.Name]; ok {
			overrides[key] = *values[f.Name]
		}
	})
	for name := range keys {
		if explicit[name] && *values[name] == "" {
			return bad("явно заданный параметр не может быть пустым; см. qag --help")
		}
	}
	if cmd == "healthcheck" {
		for name := range explicit {
			if name != "address" {
				return bad("healthcheck принимает только --address")
			}
		}
		address := *values["address"]
		if !explicit["address"] {
			address, _ = env("QAG_ADDRESS")
		}
		return healthcheckAddress(address)
	}
	if explicit["data-file"] && explicit["database-url"] {
		return bad("--data-file и --database-url несовместимы")
	}
	if explicit["storage"] {
		if *values["storage"] == "yaml" && explicit["database-url"] || *values["storage"] == "postgres" && explicit["data-file"] {
			return bad("параметры не соответствуют выбранному хранилищу")
		}
	} else if explicit["data-file"] {
		overrides["QAG_STORAGE"] = "yaml"
	} else if explicit["database-url"] {
		overrides["QAG_STORAGE"] = "postgres"
	}
	if cmd == "serve" && (explicit["input"] || explicit["output"] || explicit["dry-run"]) {
		return bad("serve не принимает параметры переноса")
	}
	if cmd == "data export" && (*output == "" || explicit["input"] || explicit["dry-run"]) {
		return bad("export требует --output FILE и не принимает --input/--dry-run")
	}
	if cmd == "data import" && (*input == "" || explicit["output"]) {
		return bad("import требует --input FILE и не принимает --output")
	}
	lookup := func(k string) (string, bool) {
		if v, ok := overrides[k]; ok {
			return v, true
		}
		return env(k)
	}
	loader := appruntime.LoadConfigEnv
	if cmd != "serve" {
		loader = appruntime.LoadDataConfigEnv
	}
	config, e := loader(lookup)
	if e != nil {
		return &commandError{2, e}
	}
	if cmd == "serve" {
		return serve(config, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var doc dataset.Document
	if cmd == "data import" {
		doc, e = datatransfer.Read(*input)
		if e != nil {
			return e
		}
	}
	var summary dataset.Summary
	if config.Storage == "yaml" {
		store, e := filestore.Open(config.DataFile, cmd == "data import")
		if e != nil {
			return e
		}
		defer store.Close()
		if cmd == "data export" {
			doc, e = store.Snapshot(ctx)
			if e == nil {
				e = datatransfer.Export(*output, doc)
			}
		} else {
			summary, e = datatransfer.ImportFile(ctx, store, *input, doc, *dry)
		}
		if e != nil {
			return reportConflicts(out, e)
		}
	} else {
		store, e := storage.Open(ctx, config.DatabaseURL, config.Namespace)
		if e != nil {
			return e
		}
		defer store.Close()
		if e = store.AcquireInstance(ctx); e != nil {
			return e
		}
		vault, e := resources.NewVault(config.SecretKey)
		if e != nil {
			return e
		}
		pg := &datatransfer.PostgreSQL{Store: store, Vault: vault}
		if cmd == "data export" {
			doc, e = pg.Snapshot(ctx)
			if e == nil {
				e = datatransfer.Export(*output, doc)
			}
		} else {
			summary, e = pg.Import(ctx, doc, *dry)
		}
		if e != nil {
			return reportConflicts(out, e)
		}
	}
	if cmd == "data export" {
		_, e = fmt.Fprintln(out, "YAML экспортирован; файл содержит открытые секреты и приватные ключи")
		return e
	}
	return json.NewEncoder(out).Encode(struct {
		DryRun      bool            `json:"dryRun"`
		Collections dataset.Summary `json:"collections"`
	}{*dry, summary})
}
func isFlag(s string) bool { return len(s) > 0 && s[0] == '-' }
func reportConflicts(out io.Writer, e error) error {
	var c *dataset.Conflicts
	if errors.As(e, &c) {
		_ = json.NewEncoder(out).Encode(struct {
			Conflicts []dataset.Conflict `json:"conflicts"`
		}{c.Items})
	}
	return e
}
