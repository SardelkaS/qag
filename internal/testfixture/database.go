package testfixture

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"qag/internal/dbscope"
)

// Database owns schemas and restricted roles in the existing qag database.
// AdminURL is kept private: it must never be passed to the app or SQL runner.
type Database struct {
	ID, Nonce                                     string
	Namespace                                     dbscope.Namespace
	AppURL, ReaderURL, TargetURL                  string
	AppRole, ReaderRole, TargetRole, TargetSchema string
	admin                                         *pgxpool.Pool
}
type ownedSchema struct {
	Name  string `json:"name"`
	OID   uint32 `json:"oid"`
	Owner string `json:"owner"`
}

func randomHex() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

type TestContext interface {
	Helper()
	Cleanup(func())
	Fatalf(string, ...any)
}

func ForTest(t TestContext, ctx context.Context) *Database {
	t.Helper()
	f, err := NewDatabase(ctx, os.Getenv("QAG_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("provision isolated test schemas: %v", err)
	}
	t.Cleanup(func() {
		clean, done := CleanupContext()
		defer done()
		if err := f.Close(clean); err != nil {
			t.Fatalf("cleanup own test schemas: %v", err)
		}
	})
	return f
}

// EnableMTLS grants a certificate-only role access to this fixture's target.
func (f *Database) EnableMTLS(ctx context.Context, dir string) (string, Certificates, error) {
	role := "qag_test_" + f.ID + "_m"
	cert, err := IssueLocalClient(dir, role)
	if err != nil {
		return "", Certificates{}, err
	}
	tx, err := f.admin.Begin(ctx)
	if err != nil {
		return "", Certificates{}, err
	}
	defer tx.Rollback(context.Background())
	var roles map[string]uint32
	if err = tx.QueryRow(ctx, `SELECT roles FROM qag_test.manifest WHERE id=$1 AND nonce=$2 FOR UPDATE`, f.ID, f.Nonce).Scan(&roles); err != nil {
		return "", Certificates{}, err
	}
	quoted := pgx.Identifier{role}.Sanitize()
	if _, err = tx.Exec(ctx, `CREATE ROLE `+quoted+` LOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS; GRANT qag_test_mtls TO `+quoted+`; GRANT USAGE ON SCHEMA `+pgx.Identifier{f.TargetSchema}.Sanitize()+` TO `+quoted+`; GRANT SELECT ON ALL TABLES IN SCHEMA `+pgx.Identifier{f.TargetSchema}.Sanitize()+` TO `+quoted, pgx.QueryExecModeSimpleProtocol); err != nil {
		return "", Certificates{}, err
	}
	var oid uint32
	if err = tx.QueryRow(ctx, `SELECT oid FROM pg_roles WHERE rolname=$1`, role).Scan(&oid); err != nil {
		return "", Certificates{}, err
	}
	roles[role] = oid
	if _, err = tx.Exec(ctx, `UPDATE qag_test.manifest SET roles=$2 WHERE id=$1`, f.ID, roles); err != nil {
		return "", Certificates{}, err
	}
	return role, cert, tx.Commit(ctx)
}
func NewDatabase(ctx context.Context, adminURL string) (*Database, error) {
	u, err := url.Parse(adminURL)
	if err != nil || strings.Trim(u.Path, "/") != "qag" || u.Host == "" {
		return nil, fmt.Errorf("test provisioner requires the existing qag database")
	}
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		return nil, err
	}
	f := &Database{ID: randomHex(), Nonce: randomHex(), admin: admin}
	app := "qag_test_" + f.ID
	f.Namespace, err = dbscope.New(app, app+"_demo", true)
	if err != nil {
		admin.Close()
		return nil, err
	}
	f.AppRole = app + "_u"
	f.ReaderRole = app + "_r"
	f.TargetRole = app + "_t"
	f.TargetSchema = app + "_target"
	tx, err := admin.Begin(ctx)
	if err != nil {
		admin.Close()
		return nil, err
	}
	defer tx.Rollback(context.Background())
	fail := func(e error) (*Database, error) { _ = tx.Rollback(context.Background()); admin.Close(); return nil, e }
	if _, err = tx.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS qag_test; CREATE TABLE IF NOT EXISTS qag_test.manifest(id text PRIMARY KEY,nonce text NOT NULL,objects jsonb NOT NULL,roles jsonb NOT NULL,created_at timestamptz NOT NULL DEFAULT now())`, pgx.QueryExecModeSimpleProtocol); err != nil {
		return fail(err)
	}
	urls := []*string{&f.AppURL, &f.ReaderURL, &f.TargetURL}
	roles := []string{f.AppRole, f.ReaderRole, f.TargetRole}
	roleIDs := map[string]uint32{}
	for i, role := range roles {
		password := randomHex() + randomHex()
		var statement string
		if err = tx.QueryRow(ctx, `SELECT format('CREATE ROLE %I LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD %L',$1::text,$2::text)`, role, password).Scan(&statement); err != nil {
			return fail(err)
		}
		if _, err = tx.Exec(ctx, statement); err != nil {
			return fail(err)
		}
		comment := "qag test role " + f.ID + ":" + f.Nonce
		if i == 1 {
			comment = "qag demo reader v1"
		}
		if err = tx.QueryRow(ctx, `SELECT format('COMMENT ON ROLE %I IS %L',$1::text,$2::text)`, role, comment).Scan(&statement); err != nil {
			return fail(err)
		}
		if _, err = tx.Exec(ctx, statement); err != nil {
			return fail(err)
		}
		var oid uint32
		if err = tx.QueryRow(ctx, `SELECT oid FROM pg_roles WHERE rolname=$1`, role).Scan(&oid); err != nil {
			return fail(err)
		}
		roleIDs[role] = oid
		child := *u
		child.User = url.UserPassword(role, password)
		q := child.Query()
		q.Set("qag.app_schema", f.Namespace.App)
		q.Set("qag.demo_schema", f.Namespace.Demo)
		child.RawQuery = q.Encode()
		*urls[i] = child.String()
	}
	objects := []ownedSchema{}
	for _, object := range []ownedSchema{{Name: app, Owner: f.AppRole}, {Name: f.Namespace.Demo, Owner: f.AppRole}, {Name: f.TargetSchema, Owner: f.TargetRole}} {
		if _, err = tx.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{object.Name}.Sanitize()+` AUTHORIZATION `+pgx.Identifier{object.Owner}.Sanitize()); err != nil {
			return fail(err)
		}
		if err = tx.QueryRow(ctx, `SELECT oid FROM pg_namespace WHERE nspname=$1`, object.Name).Scan(&object.OID); err != nil {
			return fail(err)
		}
		objects = append(objects, object)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO qag_test.manifest(id,nonce,objects,roles) VALUES($1,$2,$3,$4)`, f.ID, f.Nonce, objects, roleIDs); err != nil {
		return fail(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fail(err)
	}
	return f, nil
}
func (f *Database) Close(ctx context.Context) error {
	if f.admin == nil {
		return nil
	}
	err := f.Cleanup(ctx, f.ID, f.Nonce)
	f.admin.Close()
	f.admin = nil
	return err
}
func (f *Database) Cleanup(ctx context.Context, id, nonce string) error {
	if id != f.ID || nonce != f.Nonce || id == "" {
		return fmt.Errorf("test cleanup ownership mismatch")
	}
	tx, err := f.admin.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var database string
	if err = tx.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil || database != "qag" {
		return fmt.Errorf("test cleanup requires qag")
	}
	var objects []ownedSchema
	var roles map[string]uint32
	var stored string
	if err = tx.QueryRow(ctx, `SELECT nonce,objects,roles FROM qag_test.manifest WHERE id=$1 FOR UPDATE`, id).Scan(&stored, &objects, &roles); err != nil {
		return err
	}
	if stored != nonce {
		return fmt.Errorf("test cleanup nonce mismatch")
	}
	for _, o := range objects {
		if !strings.HasPrefix(o.Name, "qag_test_"+id) || o.Name == "qag_test" {
			return fmt.Errorf("unsafe cleanup schema")
		}
		var oid uint32
		var owner string
		if err = tx.QueryRow(ctx, `SELECT n.oid,r.rolname FROM pg_namespace n JOIN pg_roles r ON r.oid=n.nspowner WHERE n.nspname=$1`, o.Name).Scan(&oid, &owner); err != nil || oid != o.OID || owner != o.Owner {
			return fmt.Errorf("test schema identity mismatch")
		}
	}
	for role, oid := range roles {
		var actual uint32
		if !strings.HasPrefix(role, "qag_test_"+id) || tx.QueryRow(ctx, `SELECT oid FROM pg_roles WHERE rolname=$1`, role).Scan(&actual) != nil || oid != actual {
			return fmt.Errorf("test role identity mismatch")
		}
	}
	for _, o := range objects {
		if _, err = tx.Exec(ctx, `DROP SCHEMA `+pgx.Identifier{o.Name}.Sanitize()+` CASCADE`); err != nil {
			return err
		}
	}
	for role := range roles {
		if _, err = tx.Exec(ctx, `REVOKE ALL PRIVILEGES ON DATABASE qag FROM `+pgx.Identifier{role}.Sanitize()); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DROP ROLE `+pgx.Identifier{role}.Sanitize()); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM qag_test.manifest WHERE id=$1 AND nonce=$2`, id, nonce); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RenameSource frees only this fixture's schema names for a native pg_restore.
// The old objects and their OIDs remain in the same validated manifest.
func (f *Database) RenameSource(ctx context.Context) error {
	tx, err := f.admin.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var objects []ownedSchema
	var nonce string
	if err = tx.QueryRow(ctx, `SELECT nonce,objects FROM qag_test.manifest WHERE id=$1 FOR UPDATE`, f.ID).Scan(&nonce, &objects); err != nil || nonce != f.Nonce {
		return fmt.Errorf("restore manifest mismatch")
	}
	for i := range objects {
		if objects[i].Name == f.Namespace.App || objects[i].Name == f.Namespace.Demo {
			if _, err = tx.Exec(ctx, `ALTER SCHEMA `+pgx.Identifier{objects[i].Name}.Sanitize()+` RENAME TO `+pgx.Identifier{objects[i].Name + "_source"}.Sanitize()); err != nil {
				return err
			}
			objects[i].Name += "_source"
		}
	}
	_, err = tx.Exec(ctx, `UPDATE qag_test.manifest SET objects=$2 WHERE id=$1`, f.ID, objects)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (f *Database) AdoptRestored(ctx context.Context) error {
	tx, err := f.admin.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var objects []ownedSchema
	if err = tx.QueryRow(ctx, `SELECT objects FROM qag_test.manifest WHERE id=$1 AND nonce=$2 FOR UPDATE`, f.ID, f.Nonce).Scan(&objects); err != nil {
		return err
	}
	for _, name := range []string{f.Namespace.App, f.Namespace.Demo} {
		var oid uint32
		if err = tx.QueryRow(ctx, `SELECT oid FROM pg_namespace WHERE nspname=$1`, name).Scan(&oid); err != nil {
			return err
		}
		objects = append(objects, ownedSchema{Name: name, OID: oid, Owner: f.AppRole})
		if _, err = tx.Exec(ctx, `ALTER SCHEMA `+pgx.Identifier{name}.Sanitize()+` OWNER TO `+pgx.Identifier{f.AppRole}.Sanitize()); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `GRANT USAGE,CREATE ON SCHEMA `+pgx.Identifier{name}.Sanitize()+` TO `+pgx.Identifier{f.AppRole}.Sanitize()+`; GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA `+pgx.Identifier{name}.Sanitize()+` TO `+pgx.Identifier{f.AppRole}.Sanitize(), pgx.QueryExecModeSimpleProtocol); err != nil {
			return err
		}
		rows, e := tx.Query(ctx, `SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relkind IN ('r','p')`, name)
		if e != nil {
			return e
		}
		var tables []string
		for rows.Next() {
			var table string
			if e = rows.Scan(&table); e != nil {
				rows.Close()
				return e
			}
			tables = append(tables, table)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, table := range tables {
			if _, e = tx.Exec(ctx, `ALTER TABLE `+pgx.Identifier{name, table}.Sanitize()+` OWNER TO `+pgx.Identifier{f.AppRole}.Sanitize()); e != nil {
				return e
			}
		}
	}
	raw, _ := json.Marshal(objects)
	_, err = tx.Exec(ctx, `UPDATE qag_test.manifest SET objects=$2 WHERE id=$1`, f.ID, raw)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func CleanupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}
