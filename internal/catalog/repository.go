package catalog

import "github.com/SardelkaS/qag/internal/dbscope"

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/SardelkaS/qag/internal/builtin"
	"github.com/SardelkaS/qag/internal/problem"
)

type Repository struct{ pool *pgxpool.Pool }

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}

func Paginate[T any](items []T, limit int, cursor string, id func(T) string) (Page[T], error) {
	page := Page[T]{Items: []T{}}
	if limit < 1 || limit > 100 {
		return page, problem.Invalid("limit", "Размер страницы должен быть от 1 до 100")
	}
	start := 0
	if cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return page, problem.Invalid("cursor", "Некорректная страница списка")
		}
		found := false
		for i, item := range items {
			if id(item) == string(decoded) {
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			return page, problem.Invalid("cursor", "Список изменился; откройте его с первой страницы")
		}
	}
	end := min(start+limit, len(items))
	page.Items = items[start:end]
	if end < len(items) && end > start {
		page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(id(items[end-1])))
	}
	return page, nil
}

func (r *Repository) ListFolders(ctx context.Context, archived bool, limit int, cursor string) (Page[Folder], error) {
	items := []Folder{}
	rows, err := r.pool.Query(ctx, dbscope.Render(r.pool, `SELECT id::text,name,archived_at IS NOT NULL,created_at,updated_at FROM qag.folders WHERE (archived_at IS NOT NULL)=$1 AND NOT EXISTS(SELECT 1 FROM qag.builtin_demo WHERE folder_id=qag.folders.id) ORDER BY name COLLATE qag.russian, normalized_name COLLATE "C", id`), archived)
	if err != nil {
		return Page[Folder]{}, problem.Database(err)
	}
	defer rows.Close()
	for rows.Next() {
		var item Folder
		if err = rows.Scan(&item.ID, &item.Name, &item.Archived, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return Page[Folder]{}, problem.Database(err)
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return Page[Folder]{}, problem.Database(err)
	}
	return Paginate(items, limit, cursor, func(f Folder) string { return f.ID })
}

func (r *Repository) CreateFolder(ctx context.Context, value string) (Folder, error) {
	name, normalized, err := Name(value)
	if err != nil {
		return Folder{}, err
	}
	var f Folder
	err = r.pool.QueryRow(ctx, dbscope.Render(r.pool, `INSERT INTO qag.folders(name,normalized_name) VALUES($1,$2) RETURNING id::text,name,false,created_at,updated_at`), name, normalized).Scan(&f.ID, &f.Name, &f.Archived, &f.CreatedAt, &f.UpdatedAt)
	return f, problem.Database(err)
}

func (r *Repository) PatchFolder(ctx context.Context, id string, newName *string, archived *bool) (Folder, error) {
	if !ValidID(id) {
		return Folder{}, problem.Invalid("id", "Некорректный идентификатор папки")
	}
	if newName == nil && archived == nil {
		return Folder{}, problem.Invalid("name", "Нет изменений")
	}
	var f Folder
	err := r.transaction(ctx, func(tx pgx.Tx) error {
		if err := builtin.Protect(ctx, tx, id); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, dbscope.Render(tx, `SELECT id::text,name,archived_at IS NOT NULL,created_at,updated_at FROM qag.folders WHERE id=$1 FOR UPDATE`), id).Scan(&f.ID, &f.Name, &f.Archived, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return problem.Database(err)
		}
		if f.Archived && newName != nil && (archived == nil || *archived) {
			return problem.Archived()
		}
		name, normalized, err := Name(f.Name)
		if newName != nil {
			name, normalized, err = Name(*newName)
		}
		if err != nil {
			return err
		}
		wantedArchive := f.Archived
		if archived != nil {
			wantedArchive = *archived
		}
		if name == f.Name && wantedArchive == f.Archived {
			return nil
		}
		return problem.Database(tx.QueryRow(ctx, dbscope.Render(tx, `UPDATE qag.folders SET name=$2,normalized_name=$3,archived_at=CASE WHEN $4 THEN coalesce(archived_at,now()) ELSE NULL END,updated_at=now() WHERE id=$1 RETURNING id::text,name,archived_at IS NOT NULL,created_at,updated_at`), id, name, normalized, wantedArchive).Scan(&f.ID, &f.Name, &f.Archived, &f.CreatedAt, &f.UpdatedAt))
	})
	return f, err
}

const caseSelect = `SELECT c.id::text,c.folder_id::text,c.name,c.archived_at IS NOT NULL,f.archived_at IS NOT NULL,c.revision,c.steps,c.created_at,c.updated_at,EXISTS(SELECT 1 FROM qag.builtin_demo WHERE test_case_id=c.id) FROM qag.test_cases c JOIN qag.folders f ON f.id=c.folder_id`

type rowScanner interface{ Scan(...any) error }
type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func scanCase(row rowScanner) (TestCase, error) {
	var c TestCase
	err := row.Scan(&c.ID, &c.FolderID, &c.Name, &c.Archived, &c.ParentArchived, &c.Revision, &c.Steps, &c.CreatedAt, &c.UpdatedAt, &c.ReadOnly)
	if c.Steps == nil {
		c.Steps = []Step{}
	}
	c.ValidationIssues = ValidationIssues(c.Steps)
	return c, problem.Database(err)
}
func getCase(ctx context.Context, db queryer, id string, lock bool) (TestCase, error) {
	if !ValidID(id) {
		return TestCase{}, problem.Invalid("id", "Некорректный идентификатор тест-кейса")
	}
	sql := caseSelect + ` WHERE c.id=$1`
	if lock {
		sql += ` FOR UPDATE OF f,c`
	}
	return scanCase(db.QueryRow(ctx, dbscope.Render(db, sql), id))
}
func (r *Repository) GetCase(ctx context.Context, id string) (TestCase, error) {
	return getCase(ctx, r.pool, id, false)
}
func (r *Repository) DemoInfo(ctx context.Context) (builtin.Info, error) {
	return builtin.Get(ctx, r.pool)
}
func (r *Repository) CheckEnvironment(ctx context.Context, id, env string) error {
	if !ValidID(id) {
		return problem.Invalid("id", "Некорректный идентификатор тест-кейса")
	}
	if !ValidID(env) {
		return problem.Invalid("environmentId", "Выберите окружение")
	}
	return builtin.CheckEnvironment(ctx, r.pool, id, env)
}

func (r *Repository) ListCases(ctx context.Context, folderID string, limit int, cursor string) (Page[TestCase], error) {
	if !ValidID(folderID) {
		return Page[TestCase]{}, problem.Invalid("id", "Некорректный идентификатор папки")
	}
	var exists bool
	if err := r.pool.QueryRow(ctx, dbscope.Render(r.pool, `SELECT EXISTS(SELECT 1 FROM qag.folders WHERE id=$1)`), folderID).Scan(&exists); err != nil {
		return Page[TestCase]{}, problem.Database(err)
	}
	if !exists {
		return Page[TestCase]{}, problem.NotFound()
	}
	rows, err := r.pool.Query(ctx, dbscope.Render(r.pool, caseSelect+` WHERE c.folder_id=$1 ORDER BY (c.archived_at IS NOT NULL), c.name COLLATE qag.russian, c.normalized_name COLLATE "C", c.id`), folderID)
	if err != nil {
		return Page[TestCase]{}, problem.Database(err)
	}
	defer rows.Close()
	items := []TestCase{}
	for rows.Next() {
		item, err := scanCase(rows)
		if err != nil {
			return Page[TestCase]{}, err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return Page[TestCase]{}, problem.Database(err)
	}
	return Paginate(items, limit, cursor, func(c TestCase) string { return c.ID })
}

func (r *Repository) CreateCase(ctx context.Context, folderID, value string) (TestCase, error) {
	if !ValidID(folderID) {
		return TestCase{}, problem.Invalid("id", "Некорректный идентификатор папки")
	}
	var name, normalized string
	var err error
	var c TestCase
	err = r.transaction(ctx, func(tx pgx.Tx) error {
		var archived bool
		if err := builtin.Protect(ctx, tx, folderID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, dbscope.Render(tx, `SELECT archived_at IS NOT NULL FROM qag.folders WHERE id=$1 FOR UPDATE`), folderID).Scan(&archived); err != nil {
			return problem.Database(err)
		}
		if archived {
			return problem.Archived()
		}
		name, normalized, err = caseName(ctx, tx, folderID, "", value)
		if err != nil {
			return err
		}
		var id string
		if err := tx.QueryRow(ctx, dbscope.Render(tx, `INSERT INTO qag.test_cases(folder_id,name,normalized_name) VALUES($1,$2,$3) RETURNING id::text`), folderID, name, normalized).Scan(&id); err != nil {
			return problem.Database(err)
		}
		var err error
		c, err = getCase(ctx, tx, id, false)
		return err
	})
	return c, err
}

func (r *Repository) SaveCase(ctx context.Context, id, value string, revision int64, steps []Step) (TestCase, error) {
	var name, normalized string
	var err error
	if steps == nil {
		steps = []Step{}
	}
	if err = ValidateDraft(steps); err != nil {
		return TestCase{}, err
	}
	var c TestCase
	err = r.transaction(ctx, func(tx pgx.Tx) error {
		old, err := getCase(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if old.Archived || old.ParentArchived {
			return problem.Archived()
		}
		if old.ReadOnly {
			return builtin.Protect(ctx, tx, id)
		}
		if old.Revision != revision {
			return problem.Conflict("Сценарий изменён в другой вкладке; ваши правки не перезаписаны")
		}
		name, normalized, err = caseName(ctx, tx, old.FolderID, id, value)
		if err != nil {
			return err
		}
		if err = validateImageOwners(ctx, tx, id, steps); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, dbscope.Render(tx, `UPDATE qag.test_cases SET name=$2,normalized_name=$3,steps=$4,revision=revision+1,updated_at=now() WHERE id=$1`), id, name, normalized, steps)
		if err != nil {
			return problem.Database(err)
		}
		c, err = getCase(ctx, tx, id, false)
		return err
	})
	return c, err
}

func (r *Repository) PatchCase(ctx context.Context, id string, newName *string, archived *bool) (TestCase, error) {
	if newName == nil && archived == nil {
		return TestCase{}, problem.Invalid("name", "Нет изменений")
	}
	var c TestCase
	err := r.transaction(ctx, func(tx pgx.Tx) error {
		var err error
		c, err = getCase(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if c.ReadOnly {
			return builtin.Protect(ctx, tx, id)
		}
		if c.ParentArchived {
			return problem.Archived()
		}
		if c.Archived && newName != nil && (archived == nil || *archived) {
			return problem.Archived()
		}
		name, normalized, err := Name(c.Name)
		if newName != nil {
			name, normalized, err = Name(*newName)
		}
		if err != nil {
			return err
		}
		wantedArchive := c.Archived
		if archived != nil {
			wantedArchive = *archived
		}
		if name == c.Name && wantedArchive == c.Archived {
			return nil
		}
		_, err = tx.Exec(ctx, dbscope.Render(tx, `UPDATE qag.test_cases SET name=$2,normalized_name=$3,archived_at=CASE WHEN $4 THEN coalesce(archived_at,now()) ELSE NULL END,revision=revision+1,updated_at=now() WHERE id=$1`), id, name, normalized, wantedArchive)
		if err != nil {
			return problem.Database(err)
		}
		c, err = getCase(ctx, tx, id, false)
		return err
	})
	return c, err
}

func (r *Repository) transaction(ctx context.Context, apply func(pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return problem.Database(err)
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(c)
	}()
	if err = apply(tx); err != nil {
		return err
	}
	return problem.Database(tx.Commit(ctx))
}

func caseName(ctx context.Context, tx pgx.Tx, folder, id, value string) (string, string, error) {
	if strings.TrimSpace(value) != "" {
		return Name(value)
	}
	for n := 1; ; n++ {
		name := "Новый тест-кейс"
		if n > 1 {
			name = fmt.Sprintf("Новый тест-кейс %d", n)
		}
		_, norm, _ := Name(name)
		var used bool
		if err := tx.QueryRow(ctx, dbscope.Render(tx, `SELECT EXISTS(SELECT 1 FROM qag.test_cases WHERE folder_id=$1 AND normalized_name=$2 AND id::text<>$3)`), folder, norm, id).Scan(&used); err != nil {
			return "", "", problem.Database(err)
		}
		if !used {
			return name, norm, nil
		}
	}
}
