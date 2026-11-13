package resources

import "github.com/SardelkaS/qag/internal/dbscope"

import (
	"context"
	"sort"

	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/SardelkaS/qag/internal/builtin"
	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/problem"
)

type Repository struct {
	pool  *pgxpool.Pool
	vault *Vault
}

func NewRepository(pool *pgxpool.Pool, vault *Vault) *Repository { return &Repository{pool, vault} }

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}
type scanner interface{ Scan(...any) error }

const environmentSelect = `SELECT e.id::text,e.name,e.revision,v.variables,v.secret_names,EXISTS(SELECT 1 FROM qag.builtin_demo WHERE environment_id=e.id),e.description,v.http_variables FROM qag.environments e JOIN qag.environment_versions v ON v.id=e.current_version_id`

func scanEnvironment(row scanner) (Environment, error) {
	var e Environment
	err := row.Scan(&e.ID, &e.Name, &e.Revision, &e.Variables, &e.SecretNames, &e.ReadOnly, &e.Description, &e.HTTPVariables)
	if e.Variables == nil {
		e.Variables = map[string]string{}
	}
	if e.SecretNames == nil {
		e.SecretNames = []string{}
	}
	return e, problem.Database(err)
}
func (r *Repository) GetEnvironment(ctx context.Context, id string) (Environment, error) {
	if !catalog.ValidID(id) {
		return Environment{}, problem.Invalid("id", "Некорректный ID окружения")
	}
	return scanEnvironment(r.pool.QueryRow(ctx, dbscope.Render(r.pool, environmentSelect+` WHERE e.id=$1`), id))
}

func (r *Repository) ListEnvironments(ctx context.Context, limit int, cursor string) (catalog.Page[Environment], error) {
	rows, err := r.pool.Query(ctx, dbscope.Render(r.pool, environmentSelect+` ORDER BY e.name COLLATE qag.russian, e.normalized_name COLLATE "C", e.id`))
	if err != nil {
		return catalog.Page[Environment]{}, problem.Database(err)
	}
	defer rows.Close()
	items := []Environment{}
	for rows.Next() {
		item, err := scanEnvironment(rows)
		if err != nil {
			return catalog.Page[Environment]{}, err
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return catalog.Page[Environment]{}, problem.Database(rows.Err())
	}
	return catalog.Paginate(items, limit, cursor, func(e Environment) string { return e.ID })
}

func (r *Repository) SaveEnvironment(ctx context.Context, id, value string, revision int64, variables map[string]string, changes SecretChanges) (Environment, error) {
	return r.saveEnvironment(ctx, id, value, revision, variables, changes, nil)
}
func (r *Repository) SaveEnvironmentMetadata(ctx context.Context, id, name, description string, revision int64) (Environment, error) {
	return r.saveEnvironment(ctx, id, name, revision, nil, SecretChanges{}, &description)
}
func (r *Repository) SaveVariables(ctx context.Context, id string, revision int64, variables map[string]string, changes SecretChanges, sources ...map[string]catalog.HTTPHeaderSource) (Environment, error) {
	e, err := r.GetEnvironment(ctx, id)
	if err != nil {
		return e, err
	}
	return r.saveEnvironment(ctx, id, e.Name, revision, variables, changes, nil, sources...)
}
func (r *Repository) saveEnvironment(ctx context.Context, id, value string, revision int64, variables map[string]string, changes SecretChanges, description *string, sources ...map[string]catalog.HTTPHeaderSource) (Environment, error) {
	variables = NormalizeNewVariables(variables)
	name, normalized, err := catalog.Name(value)
	if err != nil {
		return Environment{}, err
	}
	if variables == nil {
		variables = map[string]string{}
	}
	create := id == ""
	if create {
		id = catalog.NewID()
	} else if !catalog.ValidID(id) {
		return Environment{}, problem.Invalid("id", "Некорректный ID окружения")
	}
	var result Environment
	err = r.transaction(ctx, func(tx pgx.Tx) error {
		httpVariables := map[string]catalog.HTTPHeaderSource{}
		if !create {
			if err := builtin.Protect(ctx, tx, id); err != nil {
				return err
			}
		}
		oldSecrets := map[string]string{}
		currentRevision := int64(0)
		if !create {
			var version string
			var sealed []byte
			var oldVariables map[string]string
			if err := tx.QueryRow(ctx, dbscope.Render(tx, `SELECT e.revision,v.id::text,v.encrypted_secrets,v.http_variables,v.variables FROM qag.environments e JOIN qag.environment_versions v ON v.id=e.current_version_id WHERE e.id=$1 FOR UPDATE OF e`), id).Scan(&currentRevision, &version, &sealed, &httpVariables, &oldVariables); err != nil {
				return problem.Database(err)
			}
			if currentRevision != revision {
				return problem.Conflict("Окружение изменилось; обновите форму")
			}
			if description != nil {
				variables = oldVariables
			}
			var err error
			oldSecrets, err = r.vault.Open(id+":"+version, sealed)
			if err != nil {
				return err
			}
		} else {
			desc := ""
			if description != nil {
				desc = *description
			}
			if _, err := tx.Exec(ctx, dbscope.Render(tx, `INSERT INTO qag.environments(id,name,normalized_name,description) VALUES($1,$2,$3,$4)`), id, name, normalized, desc); err != nil {
				return problem.Database(err)
			}
		}
		values, err := mergeSecrets(oldSecrets, changes)
		if err != nil {
			return err
		}
		if len(sources) > 0 && sources[0] != nil {
			httpVariables = NormalizeHTTPVariables(sources[0])
		}
		if err = EnvironmentVariableContext(variables, httpVariables, values); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, dbscope.Render(tx, `SELECT v.config,v.secret_names FROM qag.resources r JOIN qag.resource_versions v ON v.id=r.current_version_id WHERE r.environment_id=$1`), id)
		if err != nil {
			return problem.Database(err)
		}
		for rows.Next() {
			var config Config
			var names []string
			if err = rows.Scan(&config, &names); err != nil {
				rows.Close()
				return problem.Database(err)
			}
			if err = CheckEnvironmentNames(config, names, variables, httpVariables, values); err != nil {
				rows.Close()
				return err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return problem.Database(err)
		}
		version := catalog.NewID()
		sealed, err := r.vault.Seal(id+":"+version, values)
		if err != nil {
			return problem.Conflict("Не удалось зашифровать секреты")
		}
		next := currentRevision + 1
		if _, err = tx.Exec(ctx, dbscope.Render(tx, `INSERT INTO qag.environment_versions(id,environment_id,revision,variables,encrypted_secrets,secret_names,http_variables) VALUES($1,$2,$3,$4,$5,$6,$7)`), version, id, next, variables, sealed, secretNames(values), httpVariables); err != nil {
			return problem.Database(err)
		}
		if _, err = tx.Exec(ctx, dbscope.Render(tx, `UPDATE qag.environments SET name=$2,normalized_name=$3,revision=$4,current_version_id=$5,description=COALESCE($6::text,description),updated_at=now() WHERE id=$1`), id, name, normalized, next, version, description); err != nil {
			return problem.Database(err)
		}
		result, err = scanEnvironment(tx.QueryRow(ctx, dbscope.Render(tx, environmentSelect+` WHERE e.id=$1`), id))
		return err
	})
	return result, err
}

const resourceSelect = `SELECT r.id::text,r.environment_id::text,r.name,r.kind,v.id::text,v.revision,v.config,v.secret_names,v.created_at,EXISTS(SELECT 1 FROM qag.builtin_demo WHERE environment_id=r.environment_id) FROM qag.resources r JOIN qag.resource_versions v ON v.id=r.current_version_id`

func scanResource(row scanner) (Resource, error) {
	var result Resource
	err := row.Scan(&result.ID, &result.EnvironmentID, &result.Name, &result.Kind, &result.VersionID, &result.Revision, &result.Config, &result.SecretNames, &result.UpdatedAt, &result.ReadOnly)
	normalizeConfig(&result.Config)
	if result.SecretNames == nil {
		result.SecretNames = []string{}
	}
	return result, problem.Database(err)
}
func (r *Repository) GetResource(ctx context.Context, id string) (Resource, error) {
	if !catalog.ValidID(id) {
		return Resource{}, problem.Invalid("id", "Некорректный ID ресурса")
	}
	return scanResource(r.pool.QueryRow(ctx, dbscope.Render(r.pool, resourceSelect+` WHERE r.id=$1`), id))
}
func (r *Repository) ListResources(ctx context.Context, environmentID string, limit int, cursor string) (catalog.Page[Resource], error) {
	if _, err := r.GetEnvironment(ctx, environmentID); err != nil {
		return catalog.Page[Resource]{}, err
	}
	rows, err := r.pool.Query(ctx, dbscope.Render(r.pool, resourceSelect+` WHERE r.environment_id=$1`), environmentID)
	if err != nil {
		return catalog.Page[Resource]{}, problem.Database(err)
	}
	defer rows.Close()
	items := []Resource{}
	for rows.Next() {
		item, err := scanResource(rows)
		if err != nil {
			return catalog.Page[Resource]{}, err
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return catalog.Page[Resource]{}, problem.Database(rows.Err())
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		return items[i].ID < items[j].ID
	})
	return catalog.Paginate(items, limit, cursor, func(item Resource) string { return item.ID })
}

func (r *Repository) SaveResource(ctx context.Context, id, environmentID, name, kind string, revision int64, config Config, changes SecretChanges) (Resource, error) {
	if !variableName.MatchString(name) || len(name) > 200 {
		return Resource{}, problem.Invalid("name", "Имя ресурса: заглавные латинские буквы, цифры и _, не больше 200 символов")
	}
	normalizeConfig(&config)
	if config.TLS.Mode == "tls" && config.TLS.Certificate != "" {
		config.TLS.Mode = "mtls"
	}
	create := id == ""
	if create {
		id = catalog.NewID()
	} else if !catalog.ValidID(id) {
		return Resource{}, problem.Invalid("id", "Некорректный ID ресурса")
	}
	var result Resource
	err := r.transaction(ctx, func(tx pgx.Tx) error {
		if !create {
			if err := builtin.Protect(ctx, tx, id); err != nil {
				return err
			}
		} else if catalog.ValidID(environmentID) {
			if err := builtin.Protect(ctx, tx, environmentID); err != nil {
				return err
			}
		}
		values := map[string]string{}
		currentRevision := int64(0)
		if !create {
			var version, oldKind string
			var sealed []byte
			if err := tx.QueryRow(ctx, dbscope.Render(tx, `SELECT r.environment_id::text,r.kind,v.id::text,v.revision,v.encrypted_secrets FROM qag.resources r JOIN qag.resource_versions v ON v.id=r.current_version_id WHERE r.id=$1 FOR UPDATE OF r`), id).Scan(&environmentID, &oldKind, &version, &currentRevision, &sealed); err != nil {
				return problem.Database(err)
			}
			if currentRevision != revision {
				return problem.Conflict("Ресурс изменился; обновите форму")
			}
			if oldKind != kind {
				return problem.Invalid("kind", "Создайте отдельный ресурс для другого протокола")
			}
			var err error
			values, err = r.vault.Open(id+":"+version, sealed)
			if err != nil {
				return err
			}
		} else {
			if !catalog.ValidID(environmentID) {
				return problem.Invalid("environmentId", "Выберите окружение")
			}
			var exists bool
			if err := tx.QueryRow(ctx, dbscope.Render(tx, `SELECT EXISTS(SELECT 1 FROM qag.environments WHERE id=$1)`), environmentID).Scan(&exists); err != nil {
				return problem.Database(err)
			}
			if !exists {
				return problem.NotFound()
			}
			if kind != "http" && kind != "grpc" && kind != "postgresql" {
				return problem.Invalid("kind", "Выберите поддерживаемый протокол")
			}
			if _, err := tx.Exec(ctx, dbscope.Render(tx, `INSERT INTO qag.resources(id,environment_id,name,kind) VALUES($1,$2,$3,$4)`), id, environmentID, name, kind); err != nil {
				return problem.Database(err)
			}
		}
		values, err := mergeSecrets(values, changes)
		if err != nil {
			return err
		}
		sourceValues := map[string]string{}
		var envVersion string
		var envSealed []byte
		var envVariables map[string]string
		var envHTTP map[string]catalog.HTTPHeaderSource
		if err := tx.QueryRow(ctx, dbscope.Render(tx, `SELECT v.id::text,v.encrypted_secrets,v.variables,v.http_variables FROM qag.environments e JOIN qag.environment_versions v ON v.id=e.current_version_id WHERE e.id=$1 FOR SHARE OF e`), environmentID).Scan(&envVersion, &envSealed, &envVariables, &envHTTP); err != nil {
			return problem.Database(err)
		}
		envValues, err := r.vault.Open(environmentID+":"+envVersion, envSealed)
		if err != nil {
			return err
		}
		for k, v := range envValues {
			sourceValues[k] = v
		}
		for k, v := range values {
			sourceValues[k] = v
		}
		if err := VariableContext(config, envVariables, sourceValues, envHTTP); err != nil {
			return err
		}
		if err = ValidateConfig(kind, config, values, sourceValues); err != nil {
			return err
		}
		version := catalog.NewID()
		sealed, err := r.vault.Seal(id+":"+version, values)
		if err != nil {
			return problem.Conflict("Не удалось зашифровать секреты")
		}
		next := currentRevision + 1
		if _, err = tx.Exec(ctx, dbscope.Render(tx, `INSERT INTO qag.resource_versions(id,resource_id,revision,config,encrypted_secrets,secret_names) VALUES($1,$2,$3,$4,$5,$6)`), version, id, next, config, sealed, secretNames(values)); err != nil {
			return problem.Database(err)
		}
		if _, err = tx.Exec(ctx, dbscope.Render(tx, `UPDATE qag.resources SET name=$2,revision=$3,current_version_id=$4,updated_at=now() WHERE id=$1`), id, name, next, version); err != nil {
			return problem.Database(err)
		}
		result, err = scanResource(tx.QueryRow(ctx, dbscope.Render(tx, resourceSelect+` WHERE r.id=$1`), id))
		return err
	})
	return result, err
}

func (r *Repository) UploadProto(ctx context.Context, id string, revision int64, files map[string]string) (Resource, error) {
	resource, err := r.GetResource(ctx, id)
	if err != nil {
		return Resource{}, err
	}
	if resource.Kind != "grpc" {
		return Resource{}, problem.Invalid("kind", "Proto-bundle относится к gRPC-ресурсу")
	}
	resource.Config.ProtoFiles = files
	return r.SaveResource(ctx, id, resource.EnvironmentID, resource.Name, resource.Kind, revision, resource.Config, SecretChanges{})
}

func (r *Repository) Freeze(ctx context.Context, environmentID string, needs map[string]string) (Snapshot, error) {
	if !catalog.ValidID(environmentID) {
		return Snapshot{}, problem.Invalid("environmentId", "Выберите окружение")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Snapshot{}, problem.Database(err)
	}
	defer rollback(tx)
	snapshot := Snapshot{EnvironmentID: environmentID, ResourceVersions: map[string]string{}, Resources: []Resource{}}
	if err = tx.QueryRow(ctx, dbscope.Render(tx, `SELECT name,current_version_id::text FROM qag.environments WHERE id=$1`), environmentID).Scan(&snapshot.EnvironmentName, &snapshot.EnvironmentVersionID); err != nil {
		return Snapshot{}, problem.Database(err)
	}
	names := []string{}
	for name := range needs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		resource, err := scanResource(tx.QueryRow(ctx, dbscope.Render(tx, resourceSelect+` WHERE r.environment_id=$1 AND r.name=$2`), environmentID, name))
		if err != nil {
			return Snapshot{}, problem.Invalid("environmentId", "Ресурс "+name+" не найден в выбранном окружении")
		}
		if resource.Kind != needs[name] {
			return Snapshot{}, problem.Invalid("resource", "Несовместимый протокол ресурса "+name)
		}
		snapshot.ResourceVersions[name] = resource.VersionID
		snapshot.Resources = append(snapshot.Resources, resource)
	}
	if err = tx.Commit(ctx); err != nil {
		return Snapshot{}, problem.Database(err)
	}
	return snapshot, nil
}

func (r *Repository) Resolve(ctx context.Context, snapshot Snapshot, name string) (Resolved, error) {
	version, ok := snapshot.ResourceVersions[name]
	if !ok {
		return Resolved{}, problem.Invalid("resource", "Ресурс отсутствует в снимке прогона")
	}
	var resource Resource
	for _, item := range snapshot.Resources {
		if item.Name == name && item.VersionID == version {
			resource = item
			break
		}
	}
	if resource.ID == "" {
		return Resolved{}, problem.Conflict("Повреждён снимок ресурса")
	}
	var config Config
	var encrypted []byte
	if err := r.pool.QueryRow(ctx, dbscope.Render(r.pool, `SELECT config,encrypted_secrets FROM qag.resource_versions WHERE id=$1 AND resource_id=$2`), version, resource.ID).Scan(&config, &encrypted); err != nil {
		return Resolved{}, problem.Database(err)
	}
	secrets, err := r.vault.Open(resource.ID+":"+version, encrypted)
	if err != nil {
		return Resolved{}, err
	}
	var variables map[string]string
	var httpVariables map[string]catalog.HTTPHeaderSource
	var envEncrypted []byte
	if err = r.pool.QueryRow(ctx, dbscope.Render(r.pool, `SELECT variables,encrypted_secrets,http_variables FROM qag.environment_versions WHERE id=$1 AND environment_id=$2`), snapshot.EnvironmentVersionID, snapshot.EnvironmentID).Scan(&variables, &envEncrypted, &httpVariables); err != nil {
		return Resolved{}, problem.Database(err)
	}
	envSecrets, err := r.vault.Open(snapshot.EnvironmentID+":"+snapshot.EnvironmentVersionID, envEncrypted)
	if err != nil {
		return Resolved{}, err
	}
	for k, v := range envSecrets {
		if _, exists := secrets[k]; exists {
			return Resolved{}, problem.Invalid("secrets", "Имя секрета "+k+" повторяется в окружении и ресурсе")
		}
		secrets[k] = v
	}
	for name := range secrets {
		if _, ok := variables[name]; ok {
			return Resolved{}, problem.Invalid("variables", "Имя "+name+" одновременно открытое и секретное")
		}
	}
	resource.Config = config
	return Resolved{Resource: resource, Variables: variables, Secrets: secrets, EnvironmentID: snapshot.EnvironmentID, EnvironmentVersionID: snapshot.EnvironmentVersionID, EnvironmentHTTPVariables: httpVariables, EnvironmentVariables: variables, EnvironmentSecrets: envSecrets}, nil
}

func (r *Repository) transaction(ctx context.Context, apply func(pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return problem.Database(err)
	}
	defer rollback(tx)
	if err = apply(tx); err != nil {
		return err
	}
	return problem.Database(tx.Commit(ctx))
}
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
