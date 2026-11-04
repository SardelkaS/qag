package storage

import "github.com/SardelkaS/qag/internal/dbscope"

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

//go:embed migrations/*.sql
var migrations embed.FS

var ErrMigration = errors.New("не удалось подготовить схему PostgreSQL; проверьте доступность БД, права и совместимость версии приложения")
var ErrSchemaVersion = errors.New("схема PostgreSQL несовместима с этой версией qag")

// Migrate uses a transaction-scoped lock and checksums, so concurrent startup
// and repeated startup cannot partially apply or silently replace a migration.
func (s *Store) Migrate(ctx context.Context) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return ErrUnavailable
	}
	defer func() {
		rollback, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(rollback)
	}()
	if _, err = tx.Exec(ctx, dbscope.Render(tx, "SET LOCAL lock_timeout = '5s'")); err != nil {
		return ErrMigration
	}
	if _, err = tx.Exec(ctx, dbscope.Render(tx, "SELECT pg_advisory_xact_lock($1)"), s.Namespace.Lock(int64(0x7161676d696772))); err != nil {
		return ErrMigration
	}
	if s.Namespace != dbscope.Default {
		var own bool
		if err = tx.QueryRow(ctx, `SELECT count(*)=2 AND bool_and(nspowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)) FROM pg_namespace WHERE nspname IN ($1,$2)`, s.Namespace.App, s.Namespace.Demo).Scan(&own); err != nil || !own {
			return ErrMigration
		}
	} else if _, err = tx.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS qag`); err != nil {
		return ErrMigration
	}
	if _, err = tx.Exec(ctx, dbscope.Render(tx, `CREATE TABLE IF NOT EXISTS qag.schema_migrations (
    name text PRIMARY KEY,
    checksum text NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now()
)`), pgx.QueryExecModeSimpleProtocol); err != nil {
		return ErrMigration
	}
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return ErrMigration
	}
	sort.Strings(files)
	checksums := map[string]string{}
	contents := map[string]string{}
	for _, name := range files {
		data, err := fs.ReadFile(migrations, name)
		if err != nil {
			return ErrMigration
		}
		sum := sha256.Sum256(data)
		checksums[name] = hex.EncodeToString(sum[:])
		contents[name] = string(data)
	}
	rows, err := tx.Query(ctx, dbscope.Render(tx, "SELECT name, checksum FROM qag.schema_migrations"))
	if err != nil {
		return ErrMigration
	}
	applied := map[string]bool{}
	for rows.Next() {
		var name, checksum string
		if err = rows.Scan(&name, &checksum); err != nil {
			rows.Close()
			return ErrMigration
		}
		if expected, ok := checksums[name]; !ok || expected != checksum {
			rows.Close()
			return ErrSchemaVersion
		}
		applied[name] = true
	}
	rows.Close()
	if rows.Err() != nil {
		return ErrMigration
	}
	for _, name := range files {
		if applied[name] {
			continue
		}
		statement := dbscope.Render(tx, contents[name])
		if s.Namespace != dbscope.Default {
			statement = strings.ReplaceAll(statement, "CREATE SCHEMA "+pgx.Identifier{s.Namespace.Demo}.Sanitize()+";", "")
		}
		if _, err = tx.Exec(ctx, statement, pgx.QueryExecModeSimpleProtocol); err != nil {
			return fmt.Errorf("%w (%s)", ErrMigration, name)
		}
		if _, err = tx.Exec(ctx, dbscope.Render(tx, "INSERT INTO qag.schema_migrations(name,checksum) VALUES($1,$2)"), name, checksums[name]); err != nil {
			return ErrMigration
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return ErrMigration
	}
	return nil
}
