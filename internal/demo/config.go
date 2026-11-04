package demo

import "github.com/SardelkaS/qag/internal/dbscope"

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/resources"
)

const RoleComment = "qag demo reader v1"

func DatabaseConfig(adminURL, demoURL string) (resources.Config, map[string]string, error) {
	fail := func() (resources.Config, map[string]string, error) {
		return resources.Config{}, nil, fmt.Errorf("QAG_DEMO_DATABASE_URL должен указывать на ту же БД под отдельной ограниченной учётной записью")
	}
	a, e := url.Parse(adminURL)
	if e != nil {
		return fail()
	}
	u, e := url.Parse(demoURL)
	if e != nil || u.Host == "" || (u.Scheme != "postgres" && u.Scheme != "postgresql") || !strings.EqualFold(u.Host, a.Host) || u.Path != a.Path || u.User == nil || a.User == nil || u.User.Username() == "" || u.User.Username() == a.User.Username() {
		return fail()
	}
	pw, _ := u.User.Password()
	c := resources.Config{Address: u.Host, Database: strings.TrimPrefix(u.Path, "/"), Username: u.User.Username(), Headers: []catalog.RequestHeader{}, TLS: resources.TLSConfig{Mode: "tls"}}
	return databaseTLSConfig(c, u, pw, fail)
}
func databaseTLSConfig(c resources.Config, u *url.URL, pw string, fail func() (resources.Config, map[string]string, error)) (resources.Config, map[string]string, error) {
	secrets := map[string]string{"PASSWORD": pw}
	mode := u.Query().Get("sslmode")
	if mode == "disable" {
		c.TLS.Mode = "plaintext"
	} else if mode != "" && mode != "verify-full" {
		return fail()
	}
	c.TLS.ServerName = u.Hostname()
	for param, target := range map[string]*string{"sslrootcert": &c.TLS.CA, "sslcert": &c.TLS.Certificate} {
		if path := u.Query().Get(param); path != "" {
			b, e := os.ReadFile(path)
			if e != nil {
				return fail()
			}
			*target = string(b)
		}
	}
	if path := u.Query().Get("sslkey"); path != "" {
		b, e := os.ReadFile(path)
		if e != nil {
			return fail()
		}
		secrets["PRIVATE_KEY"] = string(b)
	}
	if err := resources.ValidateConfig("postgresql", c, secrets); err != nil {
		return fail()
	}
	return c, secrets, nil
}

// PrepareRole is called only by the explicitly run provisioning tool.
func PrepareRole(ctx context.Context, admin *pgxpool.Pool, c resources.Config, secrets map[string]string) error {
	var exists bool
	if err := admin.QueryRow(ctx, dbscope.Render(admin, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`), c.Username).Scan(&exists); err != nil {
		return fmt.Errorf("не удалось проверить demo-роль")
	}
	if exists {
		var comment string
		if err := admin.QueryRow(ctx, dbscope.Render(admin, `SELECT coalesce(shobj_description(oid,'pg_authid'),'') FROM pg_roles WHERE rolname=$1`), c.Username).Scan(&comment); err != nil || comment != RoleComment {
			return fmt.Errorf("имя demo-роли уже занято чужой ролью")
		}
		if err := checkRole(ctx, admin, c.Username); err != nil {
			return err
		}
	} else {
		var statement string
		if err := admin.QueryRow(ctx, dbscope.Render(admin, `SELECT format('CREATE ROLE %I LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD %L',$1::text,$2::text)`), c.Username, secrets["PASSWORD"]).Scan(&statement); err != nil {
			return fmt.Errorf("не удалось подготовить demo-роль")
		}
		if _, err := admin.Exec(ctx, dbscope.Render(admin, statement)); err != nil {
			return fmt.Errorf("не удалось создать demo-роль; нужны права подготовки роли")
		}
		if _, err := admin.Exec(ctx, dbscope.Render(admin, `COMMENT ON ROLE `+pgx.Identifier{c.Username}.Sanitize()+` IS 'qag demo reader v1'`)); err != nil {
			return fmt.Errorf("не удалось отметить demo-роль")
		}
	}
	if _, err := admin.Exec(ctx, dbscope.Render(admin, `GRANT CONNECT ON DATABASE `+pgx.Identifier{c.Database}.Sanitize()+` TO `+pgx.Identifier{c.Username}.Sanitize())); err != nil {
		return fmt.Errorf("не удалось выдать CONNECT demo-роли")
	}
	return nil
}
func checkRole(ctx context.Context, admin *pgxpool.Pool, name string) error {
	var safe bool
	err := admin.QueryRow(ctx, dbscope.Render(admin, `SELECT rolcanlogin AND NOT(rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls) AND NOT EXISTS(SELECT 1 FROM pg_auth_members WHERE member=r.oid) AND NOT EXISTS(SELECT 1 FROM pg_database WHERE datdba=r.oid) FROM pg_roles r WHERE rolname=$1`), name).Scan(&safe)
	if err != nil || !safe {
		return fmt.Errorf("demo-роль отсутствует или имеет повышенные права")
	}
	return nil
}
func OpenReader(ctx context.Context, admin *pgxpool.Pool, c resources.Config, secrets map[string]string) (*pgxpool.Pool, error) {
	if err := checkRole(ctx, admin, c.Username); err != nil {
		return nil, err
	}
	role := pgx.Identifier{c.Username}.Sanitize()
	if _, err := admin.Exec(ctx, dbscope.Render(admin, `GRANT USAGE ON SCHEMA qag_demo TO `+role+`; GRANT SELECT ON qag_demo.items TO `+role), pgx.QueryExecModeSimpleProtocol); err != nil {
		return nil, fmt.Errorf("не удалось подготовить права demo-схемы")
	}
	var safe bool
	ns := dbscope.From(admin)
	if err := admin.QueryRow(ctx, `SELECT NOT has_schema_privilege($1,$2,'USAGE') AND NOT has_schema_privilege($1,$3,'CREATE') AND NOT EXISTS(SELECT 1 FROM pg_tables WHERE schemaname=$2 AND has_table_privilege($1,quote_ident(schemaname)||'.'||quote_ident(tablename),'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')) AND NOT has_table_privilege($1,$4,'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')`, c.Username, ns.App, ns.Demo, pgx.Identifier{ns.Demo, "items"}.Sanitize()).Scan(&safe); err != nil || !safe {
		return nil, fmt.Errorf("demo-роль имеет доступ за пределами чтения qag_demo")
	}
	u := url.URL{Scheme: "postgresql", Host: c.Address, Path: "/" + c.Database, User: url.UserPassword(c.Username, secrets["PASSWORD"]), RawQuery: "sslmode=disable"}
	cfg, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		return nil, fmt.Errorf("некорректное demo-подключение")
	}
	cfg.ConnConfig.TLSConfig, err = resources.TLS(c, secrets)
	dbscope.Configure(cfg.ConnConfig, ns)
	cfg.ConnConfig.Fallbacks = nil
	cfg.MaxConns = 4
	if err != nil {
		return nil, fmt.Errorf("некорректные demo TLS-настройки")
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("demo-подключение недоступно")
	}
	var db string
	if err = pool.QueryRow(ctx, dbscope.Render(pool, `SELECT current_database() FROM qag_demo.items WHERE id='example-001'`)).Scan(&db); err != nil || db != c.Database {
		pool.Close()
		return nil, fmt.Errorf("demo-подключение не может прочитать пример; проверьте роль и пароль")
	}
	return pool, nil
}
