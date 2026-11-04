// Package builtin identifies protected objects without depending on repositories.
package builtin

import "github.com/SardelkaS/qag/internal/dbscope"

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/SardelkaS/qag/internal/problem"
)

type Info struct {
	TestCaseID     string `json:"testCaseId"`
	EnvironmentID  string `json:"environmentId"`
	HTTPResourceID string `json:"httpResourceId"`
	GRPCResourceID string `json:"grpcResourceId"`
	SQLResourceID  string `json:"sqlResourceId"`
}
type Querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func Get(ctx context.Context, q Querier) (Info, error) {
	var i Info
	err := q.QueryRow(ctx, dbscope.Render(q, `SELECT test_case_id::text,environment_id::text,http_resource_id::text,grpc_resource_id::text,sql_resource_id::text FROM qag.builtin_demo WHERE key='local-demo'`)).Scan(&i.TestCaseID, &i.EnvironmentID, &i.HTTPResourceID, &i.GRPCResourceID, &i.SQLResourceID)
	return i, problem.Database(err)
}
func Protect(ctx context.Context, q Querier, id string) error {
	var protected bool
	err := q.QueryRow(ctx, dbscope.Render(q, `SELECT EXISTS(SELECT 1 FROM qag.builtin_demo WHERE $1::uuid IN (folder_id,test_case_id,environment_id,http_resource_id,grpc_resource_id,sql_resource_id))`), id).Scan(&protected)
	if err != nil {
		return problem.Database(err)
	}
	if protected {
		return &problem.Error{Code: "read_only", Message: "Встроенный пример и его подключения доступны только для просмотра и запуска"}
	}
	return nil
}
func CheckEnvironment(ctx context.Context, q Querier, caseID, environmentID string) error {
	var mismatch bool
	err := q.QueryRow(ctx, dbscope.Render(q, `SELECT EXISTS(SELECT 1 FROM qag.builtin_demo WHERE test_case_id=$1 AND environment_id<>$2)`), caseID, environmentID).Scan(&mismatch)
	if err != nil {
		return problem.Database(err)
	}
	if mismatch {
		return problem.Invalid("environmentId", "Встроенный пример использует своё системное окружение")
	}
	return nil
}
