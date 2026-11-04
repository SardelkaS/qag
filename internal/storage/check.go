package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
)

// CheckSchema is read-only, including when the target has never been migrated.
func (s *Store) CheckSchema(ctx context.Context) (bool, error) {
	var exists bool
	if err := s.Pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, s.Namespace.App+".schema_migrations").Scan(&exists); err != nil {
		return false, ErrUnavailable
	}
	if !exists {
		return false, nil
	}
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return true, ErrSchemaVersion
	}
	expected := map[string]string{}
	for _, name := range files {
		b, e := fs.ReadFile(migrations, name)
		if e != nil {
			return true, ErrSchemaVersion
		}
		h := sha256.Sum256(b)
		expected[name] = hex.EncodeToString(h[:])
	}
	rows, err := s.Pool.Query(ctx, s.Namespace.SQL(`SELECT name,checksum FROM qag.schema_migrations`))
	if err != nil {
		return true, ErrUnavailable
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var name, sum string
		if rows.Scan(&name, &sum) != nil || expected[name] != sum {
			return true, ErrSchemaVersion
		}
		count++
	}
	if rows.Err() != nil {
		return true, ErrUnavailable
	}
	if count != len(expected) {
		return true, ErrSchemaVersion
	}
	return true, nil
}
