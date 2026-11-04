package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/jackc/pgx/v5"
	"os"
	"github.com/SardelkaS/qag/internal/testfixture"
	"time"
)

func main() {
	pki := flag.String("pki-dir", ".local/pki", "private local PKI directory")
	pkiOnly := flag.Bool("pki-only", false, "prepare PKI before PostgreSQL starts")
	flag.Parse()
	if err := run(*pki, *pkiOnly); err != nil {
		fmt.Fprintln(os.Stderr, "Не удалось подготовить тестовый PostgreSQL:", err)
		os.Exit(1)
	}
	fmt.Println("Локальная PKI и тестовый контекст подготовлены")
}
func run(dir string, only bool) error {
	if err := testfixture.EnsureLocalPKI(dir); err != nil {
		return err
	}
	if only {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, os.Getenv("QAG_DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("основная БД недоступна")
	}
	defer conn.Close(ctx)
	var db string
	if err = conn.QueryRow(ctx, `SELECT current_database()`).Scan(&db); err != nil || db != "qag" {
		return fmt.Errorf("нужна основная БД qag")
	}
	var exists bool
	if err = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='qag_test_mtls')`).Scan(&exists); err != nil {
		return err
	}
	if exists {
		var safe bool
		if err = conn.QueryRow(ctx, `SELECT NOT(rolcanlogin OR rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls) AND coalesce(shobj_description(oid,'pg_authid'),'')='qag local mtls group v1' FROM pg_roles WHERE rolname='qag_test_mtls'`).Scan(&safe); err != nil || !safe {
			return fmt.Errorf("имя тестовой группы занято чужой ролью")
		}
	} else {
		_, err = conn.Exec(ctx, `CREATE ROLE qag_test_mtls NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS; COMMENT ON ROLE qag_test_mtls IS 'qag local mtls group v1'`, pgx.QueryExecModeSimpleProtocol)
	}
	return err
}
