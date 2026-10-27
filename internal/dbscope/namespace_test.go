package dbscope

import (
	"strings"
	"testing"
)

func TestNamespaceValidationAndSQL(t *testing.T) {
	app := "qag_test_" + strings.Repeat("a", 32)
	n, err := New(app, app+"_demo", true)
	if err != nil {
		t.Fatal(err)
	}
	sql := `CREATE SCHEMA qag; SELECT * FROM qag.folders JOIN "qag_demo".items ON true WHERE value='qag.folders' AND body=$fn$qag.items$fn$; -- qag.folders
/* qag.demo */ SELECT $1;`
	got := n.SQL(sql)
	for _, s := range []string{`CREATE SCHEMA "` + app + `"`, `FROM "` + app + `".folders`, `"` + app + `_demo".items`, `'qag.folders'`, `$fn$qag.items$fn$`, `-- qag.folders`, `/* qag.demo */`} {
		if !strings.Contains(got, s) {
			t.Fatalf("SQL rendering lost %q", s)
		}
	}
	for _, x := range []Namespace{{}, {App: "qag", Demo: "qag_demo"}, {App: app, Demo: app}, {App: app + ";drop", Demo: app + "_demo"}} {
		if _, err := New(x.App, x.Demo, true); err == nil {
			t.Fatal("unsafe test namespace accepted")
		}
	}
	if Default.SQL(sql) != sql || Default.Lock(123) != 123 || n.Lock(123) == Default.Lock(123) {
		t.Fatal("default SQL or lock compatibility changed")
	}
}
