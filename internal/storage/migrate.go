package storage

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
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
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout = '5s'"); err != nil {
		return ErrMigration
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(0x7161676d696772)); err != nil {
		return ErrMigration
	}
	if _, err = tx.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS qag;
CREATE TABLE IF NOT EXISTS qag.schema_migrations (
    name text PRIMARY KEY,
    checksum text NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now()
)`, pgx.QueryExecModeSimpleProtocol); err != nil {
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
	rows, err := tx.Query(ctx, "SELECT name, checksum FROM qag.schema_migrations")
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
		if _, err = tx.Exec(ctx, contents[name], pgx.QueryExecModeSimpleProtocol); err != nil {
			return fmt.Errorf("%w (%s)", ErrMigration, name)
		}
		if _, err = tx.Exec(ctx, "INSERT INTO qag.schema_migrations(name,checksum) VALUES($1,$2)", name, checksums[name]); err != nil {
			return ErrMigration
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return ErrMigration
	}
	return nil
}
