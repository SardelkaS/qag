// Package dbscope builds identifiers for trusted application SQL. User SQL is
// never passed through this package.
package dbscope

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Namespace struct {
	App  string
	Demo string
}

var Default = Namespace{App: "qag", Demo: "qag_demo"}
var identifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
var testName = regexp.MustCompile(`^qag_test_[a-f0-9]{32}$`)

func New(app, demo string, test bool) (Namespace, error) {
	if !test && app == "" && demo == "" {
		return Default, nil
	}
	n := Namespace{App: app, Demo: demo}
	if !identifier.MatchString(app) || !identifier.MatchString(demo) || app == demo {
		return Namespace{}, fmt.Errorf("задайте разные допустимые схемы приложения и demo")
	}
	if test && (!testName.MatchString(app) || demo != app+"_demo") {
		return Namespace{}, fmt.Errorf("тестовый запуск требует собственные схемы qag_test_<runid>")
	}
	if !test && n != Default {
		return Namespace{}, fmt.Errorf("нестандартные схемы доступны только в явном тестовом режиме")
	}
	return n, nil
}
func Configure(c *pgx.ConnConfig, n Namespace) {
	if c.RuntimeParams == nil {
		c.RuntimeParams = map[string]string{}
	}
	c.RuntimeParams["qag.app_schema"] = n.App
	c.RuntimeParams["qag.demo_schema"] = n.Demo
}
func From(db any) Namespace {
	var c *pgx.ConnConfig
	switch v := db.(type) {
	case *pgxpool.Pool:
		c = v.Config().ConnConfig
	case *pgx.Conn:
		c = v.Config()
	case interface{ Conn() *pgx.Conn }:
		c = v.Conn().Config()
	}
	if c == nil || c.RuntimeParams["qag.app_schema"] == "" {
		return Default
	}
	return Namespace{App: c.RuntimeParams["qag.app_schema"], Demo: c.RuntimeParams["qag.demo_schema"]}
}
func (n Namespace) Lock(base int64) int64 {
	if n == Default {
		return base
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(n.App + ":" + fmt.Sprint(base)))
	return int64(h.Sum64())
}
func (n Namespace) SQL(sql string) string {
	if n == Default {
		return sql
	}
	// Lexical rendering touches identifiers only, never literals, parameters or
	// comments. Historical migration checksums are computed before this step.
	var out strings.Builder
	for i := 0; i < len(sql); {
		start := i
		switch {
		case sql[i] == '\'':
			i++
			for i < len(sql) {
				if sql[i] == '\'' {
					i++
					if i < len(sql) && sql[i] == '\'' {
						i++
						continue
					}
					break
				}
				i++
			}
			out.WriteString(sql[start:i])
		case i+1 < len(sql) && sql[i:i+2] == "--":
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			out.WriteString(sql[start:i])
		case i+1 < len(sql) && sql[i:i+2] == "/*":
			i += 2
			depth := 1
			for i < len(sql) && depth > 0 {
				if i+1 < len(sql) && sql[i:i+2] == "/*" {
					depth++
					i += 2
				} else if i+1 < len(sql) && sql[i:i+2] == "*/" {
					depth--
					i += 2
				} else {
					i++
				}
			}
			out.WriteString(sql[start:i])
		case sql[i] == '"':
			i++
			var token strings.Builder
			for i < len(sql) {
				if sql[i] == '"' {
					i++
					if i < len(sql) && sql[i] == '"' {
						token.WriteByte('"')
						i++
						continue
					}
					break
				}
				token.WriteByte(sql[i])
				i++
			}
			name := token.String()
			if name == "qag" {
				out.WriteString(pgx.Identifier{n.App}.Sanitize())
			} else if name == "qag_demo" {
				out.WriteString(pgx.Identifier{n.Demo}.Sanitize())
			} else {
				out.WriteString(sql[start:i])
			}
		case sql[i] >= 'a' && sql[i] <= 'z' || sql[i] >= 'A' && sql[i] <= 'Z' || sql[i] == '_':
			i++
			for i < len(sql) && (sql[i] >= 'a' && sql[i] <= 'z' || sql[i] >= 'A' && sql[i] <= 'Z' || sql[i] >= '0' && sql[i] <= '9' || sql[i] == '_') {
				i++
			}
			token := sql[start:i]
			if token == "qag" {
				out.WriteString(pgx.Identifier{n.App}.Sanitize())
			} else if token == "qag_demo" {
				out.WriteString(pgx.Identifier{n.Demo}.Sanitize())
			} else {
				out.WriteString(token)
			}
		case sql[i] == '$':
			// Dollar-quoted function bodies are opaque literals; placeholders are not.
			end := i + 1
			for end < len(sql) && (sql[end] == '_' || sql[end] >= 'a' && sql[end] <= 'z' || sql[end] >= 'A' && sql[end] <= 'Z' || sql[end] >= '0' && sql[end] <= '9') {
				end++
			}
			if end < len(sql) && sql[end] == '$' {
				tag := sql[i : end+1]
				if at := strings.Index(sql[end+1:], tag); at >= 0 {
					i = end + 1 + at + len(tag)
					out.WriteString(sql[start:i])
					continue
				}
			}
			out.WriteByte(sql[i])
			i++
		default:
			out.WriteByte(sql[i])
			i++
		}
	}
	return out.String()
}
func Render(db any, sql string) string { return From(db).SQL(sql) }
