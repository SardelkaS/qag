package storage

import (
	"context"
	"github.com/SardelkaS/qag/internal/dbscope"
)

// CheckImportPermissions is read-only, so dry-run never prepares schemas.
func (s *Store) CheckImportPermissions(ctx context.Context, ready bool) error {
	if s.Namespace != dbscope.Default {
		var own bool
		if err := s.Pool.QueryRow(ctx, `SELECT count(*)=2 AND bool_and(nspowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)) FROM pg_namespace WHERE nspname IN ($1,$2)`, s.Namespace.App, s.Namespace.Demo).Scan(&own); err != nil || !own {
			return ErrMigration
		}
	}
	if ready {
		var allowed bool
		err := s.Pool.QueryRow(ctx, `SELECT coalesce(bool_and(has_table_privilege(c.oid,'SELECT') AND has_table_privilege(c.oid,'INSERT') AND has_schema_privilege(n.oid,'USAGE') AND has_schema_privilege(n.oid,'CREATE')),false) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname IN ($1,$2) AND c.relkind IN ('r','p')`, s.Namespace.App, s.Namespace.Demo).Scan(&allowed)
		if err != nil || !allowed {
			return ErrMigration
		}
		return nil
	}
	var allowed bool
	err := s.Pool.QueryRow(ctx, `SELECT bool_and(CASE WHEN n.oid IS NULL THEN has_database_privilege(current_database(),'CREATE') ELSE has_schema_privilege(n.oid,'USAGE') AND has_schema_privilege(n.oid,'CREATE') AND NOT EXISTS(SELECT 1 FROM pg_class c WHERE c.relnamespace=n.oid AND c.relkind IN ('r','p','v','m','f')) END) FROM (VALUES($1::text),($2::text)) AS names(name) LEFT JOIN pg_namespace n ON n.nspname=names.name`, s.Namespace.App, s.Namespace.Demo).Scan(&allowed)
	if err != nil || !allowed {
		return ErrMigration
	}
	return nil
}
