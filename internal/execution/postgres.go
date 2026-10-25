package execution

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"qag/internal/catalog"
	"qag/internal/resources"
)

func (r *Runner) sql(ctx context.Context, b catalog.Block, res resources.Resolved) (Result, error) {
	result := Result{Protocol: "postgresql", Columns: []catalog.Column{}, Rows: [][]catalog.TypedValue{}}
	c := res.Resource.Config
	uri := url.URL{Scheme: "postgresql", Host: c.Address, Path: "/" + c.Database, User: url.UserPassword(c.Username, res.Secrets["PASSWORD"]), RawQuery: "sslmode=disable"}
	config, err := pgx.ParseConfig(uri.String())
	if err != nil {
		return result, fmt.Errorf("некорректное подключение PostgreSQL")
	}
	config.TLSConfig, err = resources.TLS(c, res.Secrets)
	if err != nil {
		return result, err
	}
	config.Fallbacks = nil
	if config.TLSConfig != nil && config.TLSConfig.ServerName == "" {
		host, _, _ := net.SplitHostPort(c.Address)
		config.TLSConfig.ServerName = host
	}
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return result, fmt.Errorf("не удалось подключиться к целевому PostgreSQL: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()
	args := []any{}
	for _, value := range b.SQL.Parameters {
		arg, err := sqlArgument(value)
		if err != nil {
			return result, err
		}
		args = append(args, arg)
	}
	// Explicit prepare rejects multiple statements even with zero parameters.
	if _, err = conn.Prepare(ctx, "qag_call", b.SQL.Query); err != nil {
		return sqlFailure(result, err)
	}
	if b.SQL.Mode == "exec" {
		tag, err := conn.Exec(ctx, "qag_call", args...)
		if err != nil {
			return sqlFailure(result, err)
		}
		affected := strconv.FormatInt(tag.RowsAffected(), 10)
		result.RowsAffected = &affected
		return result, nil
	}
	rows, err := conn.Query(ctx, "qag_call", args...)
	if err != nil {
		return sqlFailure(result, err)
	}
	defer rows.Close()
	fields := rows.FieldDescriptions()
	for _, field := range fields {
		typeName := strconv.FormatUint(uint64(field.DataTypeOID), 10)
		if typ, ok := conn.TypeMap().TypeForOID(field.DataTypeOID); ok {
			typeName = typ.Name
		}
		result.Columns = append(result.Columns, catalog.Column{Name: field.Name, Type: typeName})
	}
	bytesCaptured := 0
	for rows.Next() {
		if len(result.Rows) >= 1000 {
			result.Truncated = true
			break
		}
		values, err := rows.Values()
		if err != nil {
			return sqlFailure(result, err)
		}
		row := []catalog.TypedValue{}
		for i, value := range values {
			if rows.RawValues()[i] != nil && (fields[i].DataTypeOID == pgtype.JSONOID || fields[i].DataTypeOID == pgtype.JSONBOID) {
				raw := rows.RawValues()[i]
				if fields[i].DataTypeOID == pgtype.JSONBOID && fields[i].Format == 1 && len(raw) > 0 {
					raw = raw[1:]
				}
				row = append(row, catalog.TypedValue{Type: "json", Value: string(raw)})
				continue
			}
			row = append(row, sqlValue(value))
		}
		encoded, _ := json.Marshal(row)
		bytesCaptured += len(encoded)
		if bytesCaptured > MaxResponseBytes {
			result.Truncated = true
			break
		}
		result.Rows = append(result.Rows, row)
	}
	if !result.Truncated && rows.Err() != nil {
		return sqlFailure(result, rows.Err())
	}
	return result, nil
}

func sqlFailure(result Result, err error) (Result, error) {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		result.ErrorCode = pgError.Code
		return result, fmt.Errorf("PostgreSQL %s: %s", pgError.Code, pgError.Message)
	}
	return result, err
}
func sqlArgument(v catalog.TypedValue) (any, error) {
	if err := catalog.ValidateValue(v); err != nil {
		return nil, err
	}
	switch v.Type {
	case "null":
		return nil, nil
	case "string":
		return v.Value, nil
	case "boolean":
		return v.Value == "true", nil
	case "integer":
		if n, err := strconv.ParseInt(v.Value, 10, 64); err == nil {
			return n, nil
		}
		fallthrough
	case "decimal":
		var value pgtype.Numeric
		if err := value.Scan(v.Value); err != nil {
			return nil, fmt.Errorf("некорректное точное число")
		}
		return value, nil
	case "timestamp":
		return time.Parse(time.RFC3339Nano, v.Value)
	case "bytes":
		return base64.StdEncoding.DecodeString(v.Value)
	case "json":
		return json.RawMessage(v.Value), nil
	}
	return nil, fmt.Errorf("неподдерживаемый SQL-параметр")
}
func sqlValue(value any) catalog.TypedValue {
	if value == nil {
		return catalog.TypedValue{Type: "null"}
	}
	switch v := value.(type) {
	case string:
		return catalog.TypedValue{Type: "string", Value: v}
	case bool:
		return catalog.TypedValue{Type: "boolean", Value: strconv.FormatBool(v)}
	case int16:
		return catalog.TypedValue{Type: "integer", Value: strconv.FormatInt(int64(v), 10)}
	case int32:
		return catalog.TypedValue{Type: "integer", Value: strconv.FormatInt(int64(v), 10)}
	case int64:
		return catalog.TypedValue{Type: "integer", Value: strconv.FormatInt(v, 10)}
	case float32:
		return catalog.TypedValue{Type: "decimal", Value: strconv.FormatFloat(float64(v), 'g', -1, 32)}
	case float64:
		return catalog.TypedValue{Type: "decimal", Value: strconv.FormatFloat(v, 'g', -1, 64)}
	case pgtype.Numeric:
		val, err := v.Value()
		if err == nil {
			if text, ok := val.(string); ok {
				kind := "decimal"
				if _, valid := new(big.Rat).SetString(text); !valid {
					kind = "string"
				}
				return catalog.TypedValue{Type: kind, Value: text}
			}
		}
	case time.Time:
		return catalog.TypedValue{Type: "timestamp", Value: v.Format(time.RFC3339Nano)}
	case []byte:
		return catalog.TypedValue{Type: "bytes", Value: base64.StdEncoding.EncodeToString(v)}
	case [16]byte:
		return catalog.TypedValue{Type: "string", Value: fmt.Sprintf("%x-%x-%x-%x-%x", v[:4], v[4:6], v[6:8], v[8:10], v[10:])}
	}
	data, err := json.Marshal(value)
	if err == nil {
		return catalog.TypedValue{Type: "json", Value: string(data)}
	}
	return catalog.TypedValue{Type: "string", Value: fmt.Sprint(value)}
}
