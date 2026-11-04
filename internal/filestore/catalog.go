package filestore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/SardelkaS/qag/internal/builtin"
	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/dataset"
	"github.com/SardelkaS/qag/internal/ports"
	"github.com/SardelkaS/qag/internal/problem"
)

type Catalog struct{ Store *Store }

var _ ports.Catalog = (*Catalog)(nil)

func now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
func stamp(active bool, old *time.Time) *time.Time {
	if !active {
		return nil
	}
	if old != nil {
		return old
	}
	t := now()
	return &t
}
func index[T any](items []T, match func(T) bool) int {
	for i, v := range items {
		if match(v) {
			return i
		}
	}
	return -1
}
func (s *Store) data(ctx context.Context) (dataset.Data, error) {
	d, e := s.Snapshot(ctx)
	return d.Data, e
}
func protected(d *dataset.Data, id string) bool {
	for _, b := range d.BuiltinDemo {
		if id == b.FolderID || id == b.TestCaseID || id == b.EnvironmentID || id == b.HTTPResourceID || id == b.GRPCResourceID || id == b.SQLResourceID {
			return true
		}
	}
	return false
}
func readOnly() error {
	return &problem.Error{Code: "read_only", Message: "Встроенный пример и его подключения доступны только для просмотра и запуска"}
}
func valid(id string) error {
	if !catalog.ValidID(id) {
		return problem.Invalid("id", "Некорректный идентификатор")
	}
	return nil
}
func alphabet(a, b, idA, idB string, c *nameOrder) bool {
	if n := c.compare(a, b); n != 0 {
		return n < 0
	}
	_, na, _ := catalog.Name(a)
	_, nb, _ := catalog.Name(b)
	if na != nb {
		return na < nb
	}
	return idA < idB
}

func caseValue(d *dataset.Data, id string) (catalog.TestCase, error) {
	if err := valid(id); err != nil {
		return catalog.TestCase{}, err
	}
	i := index(d.TestCases, func(v dataset.TestCase) bool { return v.ID == id })
	if i < 0 {
		return catalog.TestCase{}, problem.NotFound()
	}
	v := d.TestCases[i]
	steps, e := dataset.Value[[]catalog.Step](v.Steps)
	if e != nil {
		return catalog.TestCase{}, storageError()
	}
	if steps == nil {
		steps = []catalog.Step{}
	}
	parent := false
	for _, f := range d.Folders {
		if f.ID == v.FolderID {
			parent = f.ArchivedAt != nil
		}
	}
	return catalog.TestCase{ID: v.ID, FolderID: v.FolderID, Name: v.Name, Archived: v.ArchivedAt != nil, ParentArchived: parent, Revision: v.Revision, Steps: steps, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, ReadOnly: protected(d, id), ValidationIssues: catalog.ValidationIssues(steps)}, nil
}
func (c *Catalog) GetCase(ctx context.Context, id string) (catalog.TestCase, error) {
	d, e := c.Store.data(ctx)
	if e != nil {
		return catalog.TestCase{}, e
	}
	return caseValue(&d, id)
}
func (c *Catalog) DemoInfo(ctx context.Context) (builtin.Info, error) {
	d, e := c.Store.data(ctx)
	if e != nil {
		return builtin.Info{}, e
	}
	if len(d.BuiltinDemo) == 0 {
		return builtin.Info{}, problem.NotFound()
	}
	b := d.BuiltinDemo[0]
	return builtin.Info{TestCaseID: b.TestCaseID, EnvironmentID: b.EnvironmentID, HTTPResourceID: b.HTTPResourceID, GRPCResourceID: b.GRPCResourceID, SQLResourceID: b.SQLResourceID}, nil
}
func (c *Catalog) CheckEnvironment(ctx context.Context, id, env string) error {
	if e := valid(id); e != nil {
		return e
	}
	if e := valid(env); e != nil {
		return e
	}
	d, e := c.Store.data(ctx)
	if e != nil {
		return e
	}
	for _, b := range d.BuiltinDemo {
		if id == b.TestCaseID && env != b.EnvironmentID {
			return problem.Invalid("environmentId", "Встроенный пример использует своё системное окружение")
		}
	}
	return nil
}
func (c *Catalog) ListFolders(ctx context.Context, archived bool, limit int, cursor string) (catalog.Page[catalog.Folder], error) {
	d, e := c.Store.data(ctx)
	if e != nil {
		return catalog.Page[catalog.Folder]{}, e
	}
	items := []catalog.Folder{}
	for _, v := range d.Folders {
		if (v.ArchivedAt != nil) == archived && !protected(&d, v.ID) {
			items = append(items, catalog.Folder{ID: v.ID, Name: v.Name, Archived: archived, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt})
		}
	}
	col := newNameOrder()
	sort.Slice(items, func(i, j int) bool { return alphabet(items[i].Name, items[j].Name, items[i].ID, items[j].ID, col) })
	return catalog.Paginate(items, limit, cursor, func(v catalog.Folder) string { return v.ID })
}
func (c *Catalog) CreateFolder(ctx context.Context, value string) (catalog.Folder, error) {
	name, norm, e := catalog.Name(value)
	if e != nil {
		return catalog.Folder{}, e
	}
	id := catalog.NewID()
	t := now()
	e = c.Store.Update(ctx, func(d *dataset.Data) error {
		for _, f := range d.Folders {
			if f.NormalizedName == norm {
				return problem.Invalid("name", "Такое имя уже занято, включая архив")
			}
		}
		d.Folders = append(d.Folders, dataset.Folder{ID: id, Name: name, NormalizedName: norm, CreatedAt: t, UpdatedAt: t})
		return nil
	})
	return catalog.Folder{ID: id, Name: name, CreatedAt: t, UpdatedAt: t}, e
}
func (c *Catalog) PatchFolder(ctx context.Context, id string, newName *string, archived *bool) (catalog.Folder, error) {
	var result catalog.Folder
	if e := valid(id); e != nil {
		return result, e
	}
	if newName == nil && archived == nil {
		return result, problem.Invalid("name", "Нет изменений")
	}
	e := c.Store.Update(ctx, func(d *dataset.Data) error {
		if protected(d, id) {
			return readOnly()
		}
		i := index(d.Folders, func(v dataset.Folder) bool { return v.ID == id })
		if i < 0 {
			return problem.NotFound()
		}
		f := &d.Folders[i]
		if f.ArchivedAt != nil && newName != nil && (archived == nil || *archived) {
			return problem.Archived()
		}
		name, norm := f.Name, f.NormalizedName
		var err error
		if newName != nil {
			name, norm, err = catalog.Name(*newName)
			if err != nil {
				return err
			}
		}
		for _, v := range d.Folders {
			if v.ID != id && v.NormalizedName == norm {
				return problem.Invalid("name", "Такое имя уже занято, включая архив")
			}
		}
		a := f.ArchivedAt != nil
		if archived != nil {
			a = *archived
		}
		if name != f.Name || a != (f.ArchivedAt != nil) {
			f.Name = name
			f.NormalizedName = norm
			f.ArchivedAt = stamp(a, f.ArchivedAt)
			f.UpdatedAt = now()
		}
		result = catalog.Folder{ID: id, Name: f.Name, Archived: a, CreatedAt: f.CreatedAt, UpdatedAt: f.UpdatedAt}
		return nil
	})
	return result, e
}
func (c *Catalog) ListCases(ctx context.Context, folderID string, limit int, cursor string) (catalog.Page[catalog.TestCase], error) {
	if e := valid(folderID); e != nil {
		return catalog.Page[catalog.TestCase]{}, e
	}
	d, e := c.Store.data(ctx)
	if e != nil {
		return catalog.Page[catalog.TestCase]{}, e
	}
	if index(d.Folders, func(v dataset.Folder) bool { return v.ID == folderID }) < 0 {
		return catalog.Page[catalog.TestCase]{}, problem.NotFound()
	}
	items := []catalog.TestCase{}
	for _, v := range d.TestCases {
		if v.FolderID == folderID {
			cv, e := caseValue(&d, v.ID)
			if e != nil {
				return catalog.Page[catalog.TestCase]{}, e
			}
			items = append(items, cv)
		}
	}
	col := newNameOrder()
	sort.Slice(items, func(i, j int) bool {
		if items[i].Archived != items[j].Archived {
			return !items[i].Archived
		}
		return alphabet(items[i].Name, items[j].Name, items[i].ID, items[j].ID, col)
	})
	return catalog.Paginate(items, limit, cursor, func(v catalog.TestCase) string { return v.ID })
}
func caseName(d *dataset.Data, folder, id, value string) (string, string, error) {
	if strings.TrimSpace(value) == "" {
		for n := 1; ; n++ {
			value = "Новый тест-кейс"
			if n > 1 {
				value = fmt.Sprintf("Новый тест-кейс %d", n)
			}
			_, norm, _ := catalog.Name(value)
			if index(d.TestCases, func(v dataset.TestCase) bool { return v.FolderID == folder && v.ID != id && v.NormalizedName == norm }) < 0 {
				break
			}
		}
	}
	name, norm, e := catalog.Name(value)
	if e == nil && index(d.TestCases, func(v dataset.TestCase) bool { return v.FolderID == folder && v.ID != id && v.NormalizedName == norm }) >= 0 {
		e = problem.Invalid("name", "Такое имя уже занято, включая архив")
	}
	return name, norm, e
}
func (c *Catalog) CreateCase(ctx context.Context, folderID, value string) (catalog.TestCase, error) {
	id := catalog.NewID()
	e := c.Store.Update(ctx, func(d *dataset.Data) error {
		if err := valid(folderID); err != nil {
			return err
		}
		if protected(d, folderID) {
			return readOnly()
		}
		i := index(d.Folders, func(v dataset.Folder) bool { return v.ID == folderID })
		if i < 0 {
			return problem.NotFound()
		}
		if d.Folders[i].ArchivedAt != nil {
			return problem.Archived()
		}
		name, norm, err := caseName(d, folderID, "", value)
		if err != nil {
			return err
		}
		t := now()
		d.TestCases = append(d.TestCases, dataset.TestCase{ID: id, FolderID: folderID, Name: name, NormalizedName: norm, Revision: 1, Steps: dataset.JSON("[]"), CreatedAt: t, UpdatedAt: t})
		return nil
	})
	if e != nil {
		return catalog.TestCase{}, e
	}
	return c.GetCase(ctx, id)
}
func checkImages(d *dataset.Data, id string, steps []catalog.Step) error {
	for _, s := range steps {
		refs := []catalog.ImageRef{}
		if s.Expected != nil {
			refs = append(refs, s.Expected.Images...)
		}
		for _, b := range s.Blocks {
			refs = append(refs, b.Expected.Images...)
		}
		for _, ref := range refs {
			if index(d.ExpectedImages, func(v dataset.Image) bool { return v.ID == ref.ImageID && v.TestCaseID == id }) < 0 {
				return problem.Invalid("images", "Изображение отсутствует или относится к другому кейсу")
			}
		}
	}
	return nil
}
func (c *Catalog) SaveCase(ctx context.Context, id, value string, revision int64, steps []catalog.Step) (catalog.TestCase, error) {
	if steps == nil {
		steps = []catalog.Step{}
	}
	if e := catalog.ValidateDraft(steps); e != nil {
		return catalog.TestCase{}, e
	}
	e := c.Store.Update(ctx, func(d *dataset.Data) error {
		old, err := caseValue(d, id)
		if err != nil {
			return err
		}
		if old.ReadOnly {
			return readOnly()
		}
		if old.Archived || old.ParentArchived {
			return problem.Archived()
		}
		if old.Revision != revision {
			return problem.Conflict("Сценарий изменён в другой вкладке; ваши правки не перезаписаны")
		}
		name, norm, err := caseName(d, old.FolderID, id, value)
		if err != nil {
			return err
		}
		if err = checkImages(d, id, steps); err != nil {
			return err
		}
		i := index(d.TestCases, func(v dataset.TestCase) bool { return v.ID == id })
		v := &d.TestCases[i]
		v.Name = name
		v.NormalizedName = norm
		v.Revision++
		v.Steps = dataset.Raw(steps)
		v.UpdatedAt = now()
		return nil
	})
	if e != nil {
		return catalog.TestCase{}, e
	}
	return c.GetCase(ctx, id)
}
func (c *Catalog) PatchCase(ctx context.Context, id string, newName *string, archived *bool) (catalog.TestCase, error) {
	if newName == nil && archived == nil {
		return catalog.TestCase{}, problem.Invalid("name", "Нет изменений")
	}
	e := c.Store.Update(ctx, func(d *dataset.Data) error {
		old, e := caseValue(d, id)
		if e != nil {
			return e
		}
		if old.ReadOnly {
			return readOnly()
		}
		if old.ParentArchived || old.Archived && newName != nil && (archived == nil || *archived) {
			return problem.Archived()
		}
		value := old.Name
		if newName != nil {
			value = *newName
		}
		name, norm, e := caseName(d, old.FolderID, id, value)
		if e != nil {
			return e
		}
		a := old.Archived
		if archived != nil {
			a = *archived
		}
		if name == old.Name && a == old.Archived {
			return nil
		}
		i := index(d.TestCases, func(v dataset.TestCase) bool { return v.ID == id })
		v := &d.TestCases[i]
		v.Name = name
		v.NormalizedName = norm
		v.ArchivedAt = stamp(a, v.ArchivedAt)
		v.Revision++
		v.UpdatedAt = now()
		return nil
	})
	if e != nil {
		return catalog.TestCase{}, e
	}
	return c.GetCase(ctx, id)
}
func (c *Catalog) UploadImage(ctx context.Context, id string, upload catalog.ImageUpload) (catalog.ExpectedImage, error) {
	meta, b, e := catalog.DecodeExpectedImage(upload)
	if e != nil {
		return meta, e
	}
	meta.ID = catalog.NewID()
	e = c.Store.Update(ctx, func(d *dataset.Data) error {
		cv, e := caseValue(d, id)
		if e != nil {
			return e
		}
		if cv.ReadOnly {
			return readOnly()
		}
		if cv.Archived || cv.ParentArchived {
			return problem.Archived()
		}
		d.ExpectedImages = append(d.ExpectedImages, dataset.Image{ID: meta.ID, TestCaseID: id, Filename: meta.Filename, ContentType: meta.ContentType, SizeBytes: meta.SizeBytes, Width: meta.Width, Height: meta.Height, SHA256: meta.SHA256, Data: b, CreatedAt: now()})
		return nil
	})
	return meta, e
}
func (c *Catalog) ReadImage(ctx context.Context, id, imageID string) (catalog.ExpectedImage, []byte, error) {
	d, e := c.Store.data(ctx)
	if e != nil {
		return catalog.ExpectedImage{}, nil, e
	}
	for _, v := range d.ExpectedImages {
		if v.ID == imageID && v.TestCaseID == id {
			return catalog.ExpectedImage{ID: v.ID, Filename: v.Filename, ContentType: v.ContentType, SizeBytes: v.SizeBytes, Width: v.Width, Height: v.Height, SHA256: v.SHA256}, v.Data, nil
		}
	}
	return catalog.ExpectedImage{}, nil, problem.NotFound()
}
func (c *Catalog) CheckImages(ctx context.Context, id string, steps []catalog.Step) error {
	d, e := c.Store.data(ctx)
	if e != nil {
		return e
	}
	return checkImages(&d, id, steps)
}
