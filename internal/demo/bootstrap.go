package demo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"qag/internal/builtin"
	"qag/internal/catalog"
	"qag/internal/problem"
	"qag/internal/resources"
)

func Bootstrap(ctx context.Context, pool *pgxpool.Pool, vault *resources.Vault, httpURL, grpcAddress string, sql resources.Config, secrets map[string]string) (builtin.Info, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return builtin.Info{}, problem.Database(err)
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(c)
	}()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(124662901)`); err != nil {
		return builtin.Info{}, problem.Database(err)
	}
	var i builtin.Info
	var folder string
	err = tx.QueryRow(ctx, `SELECT folder_id::text,test_case_id::text,environment_id::text,http_resource_id::text,grpc_resource_id::text,sql_resource_id::text FROM qag.builtin_demo WHERE key='local-demo'`).Scan(&folder, &i.TestCaseID, &i.EnvironmentID, &i.HTTPResourceID, &i.GRPCResourceID, &i.SQLResourceID)
	create := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !create {
		return i, problem.Database(err)
	}
	if create {
		folder = catalog.NewID()
		i = builtin.Info{TestCaseID: catalog.NewID(), EnvironmentID: catalog.NewID(), HTTPResourceID: catalog.NewID(), GRPCResourceID: catalog.NewID(), SQLResourceID: catalog.NewID()}
		folderName, err := unusedName(ctx, tx, "folders", "Системные примеры")
		if err != nil {
			return i, err
		}
		envName, err := unusedName(ctx, tx, "environments", "Локальный стенд qag")
		if err != nil {
			return i, err
		}
		_, folderNorm, _ := catalog.Name(folderName)
		_, envNorm, _ := catalog.Name(envName)
		if _, err = tx.Exec(ctx, `INSERT INTO qag.folders(id,name,normalized_name) VALUES($1,$2,$3)`, folder, folderName, folderNorm); err != nil {
			return i, problem.Database(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO qag.environments(id,name,normalized_name) VALUES($1,$2,$3)`, i.EnvironmentID, envName, envNorm); err != nil {
			return i, problem.Database(err)
		}
		envVersion := catalog.NewID()
		sealed, err := vault.Seal(i.EnvironmentID+":"+envVersion, map[string]string{})
		if err != nil {
			return i, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO qag.environment_versions(id,environment_id,revision,variables,encrypted_secrets,secret_names) VALUES($1,$2,1,'{}',$3,'[]')`, envVersion, i.EnvironmentID, sealed); err != nil {
			return i, problem.Database(err)
		}
		if _, err = tx.Exec(ctx, `UPDATE qag.environments SET current_version_id=$2 WHERE id=$1`, i.EnvironmentID, envVersion); err != nil {
			return i, problem.Database(err)
		}
		steps := Scenario()
		if err = catalog.ValidateSteps(steps); err != nil {
			return i, err
		}
		_, caseNorm, _ := catalog.Name(CaseName)
		if _, err = tx.Exec(ctx, `INSERT INTO qag.test_cases(id,folder_id,name,normalized_name,steps) VALUES($1,$2,$3,$4,$5)`, i.TestCaseID, folder, CaseName, caseNorm, steps); err != nil {
			return i, problem.Database(err)
		}
	}
	for _, r := range []struct {
		id, name, kind string
		c              resources.Config
		s              map[string]string
	}{
		{i.HTTPResourceID, "DEMO_HTTP", "http", resources.Config{BaseURL: httpURL + "/demo/v1", Headers: []catalog.RequestHeader{}, TLS: resources.TLSConfig{Mode: "plaintext"}}, map[string]string{}},
		{i.GRPCResourceID, "DEMO_GRPC", "grpc", resources.Config{Address: grpcAddress, Headers: []catalog.RequestHeader{}, TLS: resources.TLSConfig{Mode: "plaintext"}}, map[string]string{}},
		{i.SQLResourceID, "DEMO_SQL", "postgresql", sql, secrets},
	} {
		if err = seedResource(ctx, tx, vault, i.EnvironmentID, r.id, r.name, r.kind, r.c, r.s, create); err != nil {
			return i, err
		}
	}
	if create {
		if _, err = tx.Exec(ctx, `INSERT INTO qag.builtin_demo(key,folder_id,test_case_id,environment_id,http_resource_id,grpc_resource_id,sql_resource_id,seed_version) VALUES('local-demo',$1,$2,$3,$4,$5,$6,1)`, folder, i.TestCaseID, i.EnvironmentID, i.HTTPResourceID, i.GRPCResourceID, i.SQLResourceID); err != nil {
			return i, problem.Database(err)
		}
	}
	return i, problem.Database(tx.Commit(ctx))
}
func unusedName(ctx context.Context, tx pgx.Tx, table, base string) (string, error) {
	name := base
	for tries := 0; tries < 5; tries++ {
		_, normalized, _ := catalog.Name(name)
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qag.`+table+` WHERE normalized_name=$1)`, normalized).Scan(&exists); err != nil {
			return "", problem.Database(err)
		}
		if !exists {
			return name, nil
		}
		name = base + " " + catalog.NewID()[:8]
	}
	return "", problem.Conflict("Не удалось выбрать имя системного окружения")
}
func seedResource(ctx context.Context, tx pgx.Tx, vault *resources.Vault, env, id, name, kind string, c resources.Config, secrets map[string]string, create bool) error {
	if err := resources.ValidateConfig(kind, c, secrets); err != nil {
		return err
	}
	var revision int64
	if create {
		if _, err := tx.Exec(ctx, `INSERT INTO qag.resources(id,environment_id,name,kind) VALUES($1,$2,$3,$4)`, id, env, name, kind); err != nil {
			return problem.Database(err)
		}
	} else {
		var version string
		var oldConfig resources.Config
		var encrypted []byte
		if err := tx.QueryRow(ctx, `SELECT v.id::text,v.revision,v.config,v.encrypted_secrets FROM qag.resource_versions v JOIN qag.resources r ON r.current_version_id=v.id WHERE r.id=$1 FOR UPDATE OF r`, id).Scan(&version, &revision, &oldConfig, &encrypted); err != nil {
			return problem.Database(err)
		}
		old, err := vault.Open(id+":"+version, encrypted)
		if err != nil {
			return err
		}
		a, _ := json.Marshal(oldConfig)
		b, _ := json.Marshal(c)
		if bytes.Equal(a, b) && reflect.DeepEqual(old, secrets) {
			return nil
		}
	}
	version := catalog.NewID()
	encrypted, err := vault.Seal(id+":"+version, secrets)
	if err != nil {
		return err
	}
	names := []string{}
	for name := range secrets {
		names = append(names, name)
	}
	sort.Strings(names)
	if _, err = tx.Exec(ctx, `INSERT INTO qag.resource_versions(id,resource_id,revision,config,encrypted_secrets,secret_names) VALUES($1,$2,$3,$4,$5,$6)`, version, id, revision+1, c, encrypted, names); err != nil {
		return problem.Database(err)
	}
	_, err = tx.Exec(ctx, `UPDATE qag.resources SET current_version_id=$2,revision=$3 WHERE id=$1`, id, version, revision+1)
	return problem.Database(err)
}
