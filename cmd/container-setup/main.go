package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/SardelkaS/qag/internal/demo"
	"github.com/SardelkaS/qag/internal/deployment"
	appruntime "github.com/SardelkaS/qag/internal/runtime"
)

func main() {
	op := flag.String("operation", "init", "init, import-local, restore, export, provision")
	config := flag.String("config", "/state/config", "private configuration directory")
	pki := flag.String("pki", "/state/pki", "private PKI directory")
	postgres := flag.String("postgres-config", "/state/postgres", "PostgreSQL configuration directory")
	data := flag.String("data", "/state/data", "PostgreSQL data directory (read-only)")
	source := flag.String("source", "/source", "read-only import directory")
	output := flag.String("output", "/export", "new export directory")
	flag.Parse()
	p := deployment.Paths{Config: *config, PKI: *pki, Postgres: *postgres, Data: *data}
	var err error
	switch *op {
	case "init":
		err = deployment.Init(p)
	case "import-local":
		err = deployment.ImportLocal(p, *source)
	case "restore":
		err = deployment.Restore(p, *source)
	case "export":
		err = deployment.Export(p, *output)
	case "provision":
		err = provision()
	default:
		err = fmt.Errorf("неизвестная операция")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "qag-setup:", err)
		os.Exit(1)
	}
	fmt.Println("qag-setup: операция завершена")
}
func provision() error {
	c, err := appruntime.LoadConfigEnv(os.LookupEnv)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, c.DatabaseURL)
	if err != nil {
		return fmt.Errorf("PostgreSQL недоступна")
	}
	defer pool.Close()
	dc, secrets, err := demo.DatabaseConfig(c.DatabaseURL, c.DemoDatabaseURL)
	if err != nil {
		return err
	}
	if err = demo.PrepareRole(ctx, pool, dc, secrets); err != nil {
		return err
	}
	var exists bool
	if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='qag_test_mtls')`).Scan(&exists); err != nil {
		return fmt.Errorf("не удалось проверить mTLS-группу")
	}
	if exists {
		var safe bool
		err = pool.QueryRow(ctx, `SELECT NOT(rolcanlogin OR rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls) AND coalesce(shobj_description(oid,'pg_authid'),'')='qag local mtls group v1' FROM pg_roles WHERE rolname='qag_test_mtls'`).Scan(&safe)
		if err != nil || !safe {
			return fmt.Errorf("имя тестовой mTLS-группы занято")
		}
		return nil
	}
	if _, err = pool.Exec(ctx, `CREATE ROLE qag_test_mtls NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS`); err != nil {
		return fmt.Errorf("не удалось создать mTLS-группу")
	}
	if _, err = pool.Exec(ctx, `COMMENT ON ROLE qag_test_mtls IS 'qag local mtls group v1'`); err != nil {
		return fmt.Errorf("не удалось отметить mTLS-группу")
	}
	return nil
}
