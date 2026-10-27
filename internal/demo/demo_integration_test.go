//go:build integration

package demo

import "qag/internal/dbscope"

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"qag/internal/testfixture"

	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"qag/internal/catalog"
	demopb "qag/internal/demo/proto"
	"qag/internal/execution"
	"qag/internal/resources"
	"qag/internal/runs"
	"qag/internal/service"
	"qag/internal/storage"
)

type fixture struct {
	db      *testfixture.Database
	admin   *pgxpool.Pool
	role    string
	ctx     context.Context
	store   *storage.Store
	c       resources.Config
	secrets map[string]string
	reader  *pgxpool.Pool
	vault   *resources.Vault
}

func newFixture(t *testing.T, migrate bool) *fixture {
	t.Helper()
	ctx, done := context.WithTimeout(context.Background(), 40*time.Second)
	dbFixture := testfixture.ForTest(t, ctx)
	admin, err := pgxpool.New(ctx, os.Getenv("QAG_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dbFixture.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{ctx: ctx, store: store, role: dbFixture.ReaderRole, db: dbFixture, admin: admin}
	f.vault, _ = resources.NewVault(bytes.Repeat([]byte{3}, 32))
	t.Cleanup(func() {
		if f.reader != nil {
			f.reader.Close()
		}
		store.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = cleanup
		admin.Close()
		done()
	})
	if !migrate {
		return f
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	f.c, f.secrets, err = DatabaseConfig(dbFixture.AppURL, dbFixture.ReaderURL)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = PrepareRole(ctx, admin, f.c, f.secrets); err != nil {
			t.Fatal(err)
		}
	}
	f.reader, err = OpenReader(ctx, store.Pool, f.c, f.secrets)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func TestDemoProtocolsBootstrapAndProtection(t *testing.T) {
	f := newFixture(t, true)
	s := &Service{Pool: f.reader}
	httpTarget := httptest.NewServer(s.Handler())
	defer httpTarget.Close()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	g := grpc.NewServer()
	s.RegisterGRPC(g)
	go g.Serve(l)
	defer g.Stop()
	c := catalog.NewRepository(f.store.Pool)
	res := resources.NewRepository(f.store.Pool, f.vault)
	history := runs.NewRepository(f.store.Pool)
	engine := service.New(f.ctx, c, res, history)
	defer engine.Close()
	if _, err = c.CreateFolder(f.ctx, "Системные примеры"); err != nil {
		t.Fatal(err)
	}
	if _, err = res.SaveEnvironment(f.ctx, "", "Локальный стенд qag", 0, nil, resources.SecretChanges{}); err != nil {
		t.Fatal(err)
	}
	i, err := Bootstrap(f.ctx, f.store.Pool, f.vault, httpTarget.URL, l.Addr().String(), f.c, f.secrets)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Bootstrap(f.ctx, f.store.Pool, f.vault, httpTarget.URL, l.Addr().String(), f.c, f.secrets)
	if err != nil || i != second {
		t.Fatal("bootstrap not idempotent", err)
	}
	caseDoc, err := c.GetCase(f.ctx, i.TestCaseID)
	if err != nil || !caseDoc.ReadOnly || len(caseDoc.Steps) != 5 {
		t.Fatal("missing read-only example", err)
	}
	folders, err := c.ListFolders(f.ctx, false, 100, "")
	if err != nil || len(folders.Items) != 1 {
		t.Fatal("system folder leaked")
	}
	for _, call := range []func() error{
		func() error {
			_, e := c.SaveCase(f.ctx, caseDoc.ID, caseDoc.Name, caseDoc.Revision, caseDoc.Steps)
			return e
		},
		func() error { flag := true; _, e := c.PatchCase(f.ctx, caseDoc.ID, nil, &flag); return e },
		func() error { flag := true; _, e := c.PatchFolder(f.ctx, caseDoc.FolderID, nil, &flag); return e },
		func() error { _, e := c.CreateCase(f.ctx, caseDoc.FolderID, "Forbidden"); return e },
		func() error {
			env, e := res.GetEnvironment(f.ctx, i.EnvironmentID)
			if e != nil {
				return e
			}
			_, e = res.SaveEnvironment(f.ctx, env.ID, env.Name, env.Revision, nil, resources.SecretChanges{})
			return e
		},
		func() error {
			r, e := res.GetResource(f.ctx, i.HTTPResourceID)
			if e != nil {
				return e
			}
			_, e = res.SaveResource(f.ctx, r.ID, r.EnvironmentID, r.Name, r.Kind, r.Revision, r.Config, resources.SecretChanges{})
			return e
		},
	} {
		if err = call(); err == nil {
			t.Fatal("protected mutation succeeded")
		}
	}
	snapshot, err := res.Freeze(f.ctx, i.EnvironmentID, map[string]string{"DEMO_HTTP": "http", "DEMO_GRPC": "grpc", "DEMO_SQL": "postgresql"})
	if err != nil {
		t.Fatal(err)
	}
	runner := execution.NewRunner()
	for _, step := range caseDoc.Steps {
		for _, b := range step.Blocks {
			resolved, e := res.Resolve(f.ctx, snapshot, b.Resource)
			if e != nil {
				t.Fatal(e)
			}
			prepared, connection, e := resolved.Prepare(b)
			if e != nil {
				t.Fatal(e)
			}
			state, result := runner.Execute(f.ctx, "demo", prepared, connection)
			if state != "completed" || result.Comparison.Status != "match" {
				t.Fatalf("%s: %s %+v", b.Kind, state, result)
			}
		}
	}
	for _, query := range []string{`SELECT * FROM qag.folders`, `SELECT * FROM qag.resource_versions`, `UPDATE qag_demo.items SET name='bad'`, `CREATE TABLE qag_demo.bad(id int)`} {
		if _, err = f.reader.Exec(f.ctx, dbscope.Render(f.reader, query)); err == nil {
			t.Fatal("demo role exceeded scope")
		}
	}
	var amount string
	if err = f.reader.QueryRow(f.ctx, dbscope.Render(f.reader, `SELECT amount::text FROM qag_demo.items`)).Scan(&amount); err != nil || amount != "9007199254740993.125" {
		t.Fatal("demo row modified")
	}
	run, err := engine.StartRun(f.ctx, caseDoc.ID, i.EnvironmentID, caseDoc.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = engine.StartRun(f.ctx, caseDoc.ID, catalog.NewID(), caseDoc.Revision, catalog.NewID()); err == nil {
		t.Fatal("wrong environment accepted")
	}
	before, err := res.GetResource(f.ctx, i.HTTPResourceID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Bootstrap(f.ctx, f.store.Pool, f.vault, httpTarget.URL+"/changed", l.Addr().String(), f.c, f.secrets)
	if err != nil {
		t.Fatal(err)
	}
	after, err := res.GetResource(f.ctx, i.HTTPResourceID)
	if err != nil || after.Revision != before.Revision+1 {
		t.Fatal("resource not versioned")
	}
	old, err := history.Get(f.ctx, run.ID)
	if err != nil || !reflect.DeepEqual(old.Resources, run.Resources) {
		t.Fatal("run snapshot changed")
	}
	for _, id := range []string{i.GRPCResourceID, i.SQLResourceID} {
		r, e := res.GetResource(f.ctx, id)
		if e != nil || r.Revision != 1 {
			t.Fatal("unchanged resource version increased")
		}
	}
	public, _ := json.Marshal(after)
	if bytes.Contains(public, []byte(f.secrets["PASSWORD"])) {
		t.Fatal("secret in public DTO")
	}
	if _, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `DROP TABLE qag_demo.items`)); err != nil {
		t.Fatal(err)
	}
	r, err := httpTarget.Client().Get(httpTarget.URL + "/demo/v1/items/example-001")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 503 {
		t.Fatal("DB error became success")
	}
	if _, err = s.GetItem(f.ctx, &demopb.GetItemRequest{Id: "example-001"}); status.Code(err) != codes.Unavailable {
		t.Fatal("gRPC DB error became success")
	}
}

func TestDemoRoleDoesNotDowngradeForeignOrPrivilegedRole(t *testing.T) {
	f := newFixture(t, true)
	if _, err := f.admin.Exec(f.ctx, `ALTER ROLE `+pgx.Identifier{f.c.Username}.Sanitize()+` CREATEDB`); err != nil {
		t.Fatal(err)
	}
	if err := PrepareRole(f.ctx, f.store.Pool, f.c, f.secrets); err == nil {
		t.Fatal("privileged role adopted")
	}
	var stillPrivileged bool
	_ = f.store.Pool.QueryRow(f.ctx, dbscope.Render(f.store.Pool, `SELECT rolcreatedb FROM pg_roles WHERE rolname=$1`), f.c.Username).Scan(&stillPrivileged)
	if !stillPrivileged {
		t.Fatal("role unexpectedly downgraded")
	}
	_, _ = f.admin.Exec(f.ctx, `ALTER ROLE `+pgx.Identifier{f.c.Username}.Sanitize()+` NOCREATEDB; COMMENT ON ROLE `+pgx.Identifier{f.c.Username}.Sanitize()+` IS 'foreign role'`, pgx.QueryExecModeSimpleProtocol)
	if err := PrepareRole(f.ctx, f.store.Pool, f.c, f.secrets); err == nil {
		t.Fatal("foreign role adopted")
	}
}
func TestDemoBootstrapRollbackAndSchemaConflict(t *testing.T) {
	t.Run("rollback", func(t *testing.T) {
		f := newFixture(t, true)
		_, err := f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `CREATE FUNCTION qag.fail_demo_seed() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RAISE EXCEPTION ''test failure''; END'; CREATE TRIGGER fail_demo_seed BEFORE INSERT ON qag.builtin_demo FOR EACH ROW EXECUTE FUNCTION qag.fail_demo_seed()`))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = Bootstrap(f.ctx, f.store.Pool, f.vault, "http://127.0.0.1:1", "127.0.0.1:2", f.c, f.secrets); err == nil {
			t.Fatal("seed unexpectedly succeeded")
		}
		for _, table := range []string{"folders", "test_cases", "environments", "resources", "builtin_demo"} {
			var n int
			if err = f.store.Pool.QueryRow(f.ctx, dbscope.Render(f.store.Pool, `SELECT count(*) FROM qag.`+table)).Scan(&n); err != nil || n != 0 {
				t.Fatal("partial seed remained", table)
			}
		}
		_, _ = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `DROP TRIGGER fail_demo_seed ON qag.builtin_demo;DROP FUNCTION qag.fail_demo_seed()`))
		if _, err = Bootstrap(f.ctx, f.store.Pool, f.vault, "http://127.0.0.1:1", "127.0.0.1:2", f.c, f.secrets); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("foreign schema", func(t *testing.T) {
		f := newFixture(t, false)
		if _, err := f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `CREATE TABLE qag_demo.sentinel(value text); INSERT INTO qag_demo.sentinel VALUES('keep')`)); err != nil {
			t.Fatal(err)
		}
		if _, err := f.admin.Exec(f.ctx, `ALTER SCHEMA `+pgx.Identifier{f.store.Namespace.Demo}.Sanitize()+` OWNER TO `+pgx.Identifier{f.db.TargetRole}.Sanitize()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = f.admin.Exec(context.Background(), `ALTER SCHEMA `+pgx.Identifier{f.store.Namespace.Demo}.Sanitize()+` OWNER TO `+pgx.Identifier{f.db.AppRole}.Sanitize())
		})
		if err := f.store.Migrate(f.ctx); err == nil {
			t.Fatal("foreign schema adopted")
		}
		var value string
		if err := f.admin.QueryRow(f.ctx, f.store.Namespace.SQL(`SELECT value FROM qag_demo.sentinel`)).Scan(&value); err != nil || value != "keep" {
			t.Fatal("foreign schema changed")
		}
	})
}
func TestScenarioIsComplete(t *testing.T) {
	steps := Scenario()
	if err := catalog.ValidateSteps(steps); err != nil {
		t.Fatal(err)
	}
	if len(steps) != 5 {
		t.Fatal(fmt.Sprint(len(steps)))
	}
}

func TestUpgradePreservesUserCaseAndHistory(t *testing.T) {
	f := newFixture(t, false)
	if _, err := f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `CREATE TABLE qag.schema_migrations(name text PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())`)); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"001_initial.sql", "002_russian_collation.sql"} {
		data, err := os.ReadFile("../storage/migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, string(data)), pgx.QueryExecModeSimpleProtocol); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if _, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `INSERT INTO qag.schema_migrations(name,checksum) VALUES($1,$2)`), "migrations/"+name, hex.EncodeToString(sum[:])); err != nil {
			t.Fatal(err)
		}
	}
	folder, caseID, step, env, version := catalog.NewID(), catalog.NewID(), catalog.NewID(), catalog.NewID(), catalog.NewID()
	doc := catalog.TestCase{ID: caseID, FolderID: folder, Name: "До обновления", Revision: 1, Steps: []catalog.Step{{ID: step, Description: "Сохранить при обновлении", Blocks: []catalog.Block{}}}}
	if _, err := f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `INSERT INTO qag.folders(id,name,normalized_name) VALUES($1,'Моя папка','моя папка')`), folder); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `INSERT INTO qag.test_cases(id,folder_id,name,normalized_name,steps) VALUES($1,$2,$3,$4,$5)`), caseID, folder, doc.Name, "до обновления", doc.Steps); err != nil {
		t.Fatal(err)
	}
	tx, err := f.store.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(f.ctx, dbscope.Render(tx, `INSERT INTO qag.environments(id,name,normalized_name) VALUES($1,'До обновления','до обновления')`), env); err != nil {
		t.Fatal(err)
	}
	sealed, _ := f.vault.Seal(env+":"+version, map[string]string{})
	if _, err = tx.Exec(f.ctx, dbscope.Render(tx, `INSERT INTO qag.environment_versions(id,environment_id,revision,variables,encrypted_secrets,secret_names) VALUES($1,$2,1,'{}',$3,'[]')`), version, env, sealed); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, dbscope.Render(tx, `UPDATE qag.environments SET current_version_id=$2 WHERE id=$1`), env, version); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	history := runs.NewRepository(f.store.Pool)
	runID := catalog.NewID()
	frozen := resources.Snapshot{EnvironmentID: env, EnvironmentName: "До обновления", EnvironmentVersionID: version, Resources: []resources.Resource{}, ResourceVersions: map[string]string{}}
	if _, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `INSERT INTO qag.runs(id,test_case_id,environment_version_id,snapshot,context,idempotency_key,request_hash,status,retention_reasons,finished_at) VALUES($1,$2,$3,$4,$5,$6,$7,'passed',ARRAY['first_run','first_success'],clock_timestamp())`), runID, caseID, version, doc, frozen, catalog.NewID(), strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `INSERT INTO qag.run_steps(run_id,step_id,status,comment) VALUES($1,$2,'passed','Комментарий до обновления')`), runID, step); err != nil {
		t.Fatal(err)
	}
	rawHistory := func() string {
		var raw string
		if err := f.store.Pool.QueryRow(f.ctx, dbscope.Render(f.store.Pool, `SELECT jsonb_build_object('run',(SELECT to_jsonb(r) FROM qag.runs r WHERE id=$1),'steps',(SELECT jsonb_agg(to_jsonb(s)) FROM qag.run_steps s WHERE run_id=$1))::text`), runID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := rawHistory()
	if err = f.store.Migrate(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.c, f.secrets, err = DatabaseConfig(f.db.AppURL, f.db.ReaderURL)
	if err != nil {
		t.Fatal(err)
	}
	f.reader, err = OpenReader(f.ctx, f.store.Pool, f.c, f.secrets)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Bootstrap(f.ctx, f.store.Pool, f.vault, "http://127.0.0.1:1", "127.0.0.1:2", f.c, f.secrets); err != nil {
		t.Fatal(err)
	}
	after, err := history.Get(f.ctx, runID)
	if err != nil || before != rawHistory() || after.StepStates[0].Comment != "Комментарий до обновления" {
		t.Fatal("upgrade changed history", err)
	}
	got, err := catalog.NewRepository(f.store.Pool).GetCase(f.ctx, caseID)
	if err != nil || got.Name != doc.Name || got.Revision != 1 || got.ReadOnly || got.Steps[0].Description != doc.Steps[0].Description {
		t.Fatal("upgrade changed user case")
	}
}
