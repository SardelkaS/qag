package resources

import (
	"context"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/dbscope"
	"github.com/SardelkaS/qag/internal/problem"
	"github.com/jackc/pgx/v5"
)

// ResolveProbe reads a consistent current owner context, including keep secrets,
// without requiring a valid target connection or performing network requests.
func (r *Repository) ResolveProbe(ctx context.Context, envID, resourceID string) (Resolved, error) {
	var out Resolved
	if !catalog.ValidID(envID) || resourceID != "" && !catalog.ValidID(resourceID) {
		return out, problem.Invalid("environmentId", "Неверный ID владельца")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, problem.Database(err)
	}
	defer rollback(tx)
	var encrypted []byte
	out.EnvironmentID = envID
	if err = tx.QueryRow(ctx, dbscope.Render(tx, `SELECT e.name,e.revision,v.id::text,v.variables,v.http_variables,v.encrypted_secrets FROM qag.environments e JOIN qag.environment_versions v ON v.id=e.current_version_id WHERE e.id=$1`), envID).Scan(&out.EnvironmentName, &out.EnvironmentRevision, &out.EnvironmentVersionID, &out.EnvironmentVariables, &out.EnvironmentHTTPVariables, &encrypted); err != nil {
		return out, problem.Database(err)
	}
	out.EnvironmentSecrets, err = r.vault.Open(envID+":"+out.EnvironmentVersionID, encrypted)
	if err != nil {
		return out, err
	}
	out.Variables = out.EnvironmentVariables
	out.Secrets = map[string]string{}
	for k, v := range out.EnvironmentSecrets {
		out.Secrets[k] = v
	}
	if resourceID != "" {
		out.Resource, err = scanResource(tx.QueryRow(ctx, dbscope.Render(tx, resourceSelect+` WHERE r.id=$1 AND r.environment_id=$2`), resourceID, envID))
		if err != nil {
			return out, err
		}
		if err = tx.QueryRow(ctx, dbscope.Render(tx, `SELECT encrypted_secrets FROM qag.resource_versions WHERE id=$1`), out.Resource.VersionID).Scan(&encrypted); err != nil {
			return out, problem.Database(err)
		}
		local, err := r.vault.Open(resourceID+":"+out.Resource.VersionID, encrypted)
		if err != nil {
			return out, err
		}
		for k, v := range local {
			if _, exists := out.Secrets[k]; exists {
				return out, problem.Invalid("secrets", "Имя секрета повторяется в окружении и ресурсе")
			}
			out.Secrets[k] = v
		}
	}
	return out, problem.Database(tx.Commit(ctx))
}

// ProbeSource restricts validation to the selected source and the static values
// it needs. Unrelated unfinished HTTP definitions in a draft are not executed.
func ProbeSource(res Resolved, source catalog.HTTPHeaderSource) (catalog.HTTPHeaderSource, error) {
	// Only mode/name are needed to reject dynamic dependencies. Do not validate
	// unrelated unfinished HTTP forms from the same authoring draft.
	dummy := catalog.HTTPHeaderSource{Method: "GET", URL: "http://127.0.0.1/", TLS: catalog.TLSConfig{Mode: "plaintext"}, BodyMode: "none", FetchMode: "per_attempt"}
	locals := map[string]VariableDefinition{}
	for name, def := range NormalizeResourceVariables(res.Resource.Config.Variables) {
		if def.Mode == "http" {
			def.Source = &dummy
			def.Value = ""
		}
		locals[name] = def
	}
	res.Resource.Config.Variables = locals
	envSources := map[string]catalog.HTTPHeaderSource{}
	for name := range NormalizeHTTPVariables(res.EnvironmentHTTPVariables) {
		envSources[name] = dummy
	}
	res.EnvironmentHTTPVariables = envSources
	prepared, err := res.withVariables()
	if err != nil {
		return source, err
	}
	res = prepared
	res.forbidDynamic = true
	return res.PrepareVariableSource(source)
}
