package filestore

import (
	"context"
	"maps"
	"reflect"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/dataset"
	"github.com/SardelkaS/qag/internal/demo"
	"github.com/SardelkaS/qag/internal/resources"
	"github.com/jackc/pgx/v5"
)

var _ demo.ItemReader = (*Store)(nil)

func (s *Store) ReadItem(ctx context.Context, id string) (demo.Item, error) {
	d, e := s.data(ctx)
	if e != nil {
		return demo.Item{}, e
	}
	for _, v := range d.DemoItems {
		if v.ID == id {
			return demo.Item{ID: v.ID, Name: v.Name, Status: v.Status, Amount: v.Amount}, nil
		}
	}
	return demo.Item{}, pgx.ErrNoRows
}
func (s *Store) Ready(ctx context.Context) error { _, e := s.ReadItem(ctx, "example-001"); return e }
func (s *Store) Bootstrap(ctx context.Context, httpURL, grpcAddress string) error {
	return s.Update(ctx, func(d *dataset.Data) error {
		if index(d.DemoItems, func(v dataset.DemoItem) bool { return v.ID == "example-001" }) < 0 {
			d.DemoItems = append(d.DemoItems, dataset.DemoItem{ID: "example-001", Name: "Локальный пример qag", Status: "ready", Amount: "9007199254740993.125"})
		}
		var b dataset.Builtin
		if len(d.BuiltinDemo) == 0 {
			b = dataset.Builtin{Key: "local-demo", FolderID: catalog.NewID(), TestCaseID: catalog.NewID(), EnvironmentID: catalog.NewID(), HTTPResourceID: catalog.NewID(), GRPCResourceID: catalog.NewID(), SQLResourceID: catalog.NewID(), SeedVersion: 1}
			fname := unusedName("Системные примеры", func(name string) bool {
				_, norm, _ := catalog.Name(name)
				return index(d.Folders, func(v dataset.Folder) bool { return v.NormalizedName == norm }) >= 0
			})
			_, norm, _ := catalog.Name(fname)
			t := now()
			d.Folders = append(d.Folders, dataset.Folder{ID: b.FolderID, Name: fname, NormalizedName: norm, CreatedAt: t, UpdatedAt: t})
			ename := unusedName("Локальный стенд qag", func(name string) bool {
				_, norm, _ := catalog.Name(name)
				return index(d.Environments, func(v dataset.Environment) bool { return v.NormalizedName == norm }) >= 0
			})
			_, norm, _ = catalog.Name(ename)
			version := catalog.NewID()
			d.Environments = append(d.Environments, dataset.Environment{ID: b.EnvironmentID, Name: ename, NormalizedName: norm, Revision: 1, CurrentVersionID: version, CreatedAt: t, UpdatedAt: t})
			d.EnvironmentVersions = append(d.EnvironmentVersions, dataset.EnvironmentVersion{ID: version, EnvironmentID: b.EnvironmentID, Revision: 1, Variables: map[string]string{}, Secrets: map[string]string{}, SecretNames: []string{}, CreatedAt: t})
			_, norm, _ = catalog.Name(demo.CaseName)
			d.TestCases = append(d.TestCases, dataset.TestCase{ID: b.TestCaseID, FolderID: b.FolderID, Name: demo.CaseName, NormalizedName: norm, Revision: 1, Steps: dataset.Raw(demo.Scenario()), CreatedAt: t, UpdatedAt: t})
			d.BuiltinDemo = append(d.BuiltinDemo, b)
		} else {
			b = d.BuiltinDemo[0]
		}
		ci := index(d.TestCases, func(v dataset.TestCase) bool { return v.ID == b.TestCaseID })
		if ci >= 0 {
			steps, e := dataset.Value[[]catalog.Step](d.TestCases[ci].Steps)
			if e != nil {
				return e
			}
			changed := false
			query := "SELECT id, name, status, amount FROM qag_demo.items WHERE id=$1 ORDER BY id"
			for si := range steps {
				for bi := range steps[si].Blocks {
					block := &steps[si].Blocks[bi]
					if block.Resource == "DEMO_SQL" && block.SQL != nil && block.SQL.Query != query {
						block.SQL.Query = query
						changed = true
					}
				}
			}
			if changed {
				d.TestCases[ci].Steps = dataset.Raw(steps)
				d.TestCases[ci].Revision++
				d.TestCases[ci].UpdatedAt = now()
			}
		}
		for _, r := range []struct {
			id, name, kind string
			config         resources.Config
		}{
			{b.HTTPResourceID, "DEMO_HTTP", "http", resources.Config{BaseURL: httpURL + "/demo/v1", Headers: []catalog.RequestHeader{}, TLS: resources.TLSConfig{Mode: "plaintext"}}},
			{b.GRPCResourceID, "DEMO_GRPC", "grpc", resources.Config{Address: grpcAddress, Headers: []catalog.RequestHeader{}, TLS: resources.TLSConfig{Mode: "plaintext"}}},
			{b.SQLResourceID, "DEMO_SQL", "postgresql", resources.Config{UnavailableReason: "demo_sql_unavailable", Headers: []catalog.RequestHeader{}, TLS: resources.TLSConfig{Mode: "plaintext"}}},
		} {
			i := index(d.Resources, func(v dataset.Resource) bool { return v.ID == r.id })
			if i < 0 {
				t := now()
				d.Resources = append(d.Resources, dataset.Resource{ID: r.id, EnvironmentID: b.EnvironmentID, Name: r.name, Kind: r.kind, CreatedAt: t, UpdatedAt: t})
				i = len(d.Resources) - 1
			}
			old := d.Resources[i]
			j := index(d.ResourceVersions, func(v dataset.ResourceVersion) bool { return v.ID == old.CurrentVersionID })
			resources.NormalizeConfig(&r.config)
			if j >= 0 {
				prev := d.ResourceVersions[j]
				config, e := dataset.Value[resources.Config](prev.Config)
				resources.NormalizeConfig(&config)
				if e == nil && reflect.DeepEqual(config, r.config) && maps.Equal(prev.Secrets, map[string]string{}) {
					continue
				}
			}
			v := &d.Resources[i]
			v.Revision++
			v.CurrentVersionID = catalog.NewID()
			v.UpdatedAt = now()
			d.ResourceVersions = append(d.ResourceVersions, dataset.ResourceVersion{ID: v.CurrentVersionID, ResourceID: v.ID, Revision: v.Revision, Config: dataset.Raw(r.config), Secrets: map[string]string{}, SecretNames: []string{}, CreatedAt: now()})
		}
		return nil
	})
}
func unusedName(base string, used func(string) bool) string {
	if !used(base) {
		return base
	}
	for {
		v := base + " " + catalog.NewID()[:8]
		if !used(v) {
			return v
		}
	}
}
