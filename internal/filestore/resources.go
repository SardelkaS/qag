package filestore

import (
	"context"
	"sort"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/dataset"
	"github.com/SardelkaS/qag/internal/ports"
	"github.com/SardelkaS/qag/internal/problem"
	"github.com/SardelkaS/qag/internal/resources"
)

type Resources struct{ Store *Store }

var _ ports.Resources = (*Resources)(nil)

func environment(d *dataset.Data, id string) (resources.Environment, error) {
	if e := valid(id); e != nil {
		return resources.Environment{}, e
	}
	i := index(d.Environments, func(v dataset.Environment) bool { return v.ID == id })
	if i < 0 {
		return resources.Environment{}, problem.NotFound()
	}
	v := d.Environments[i]
	j := index(d.EnvironmentVersions, func(e dataset.EnvironmentVersion) bool { return e.ID == v.CurrentVersionID })
	if j < 0 {
		return resources.Environment{}, storageError()
	}
	ver := d.EnvironmentVersions[j]
	vars := ver.Variables
	if vars == nil {
		vars = map[string]string{}
	}
	return resources.Environment{ID: id, Name: v.Name, Description: v.Description, Revision: v.Revision, Variables: vars, SecretNames: resources.SecretNames(ver.Secrets), ReadOnly: protected(d, id)}, nil
}
func resource(d *dataset.Data, id, version string) (resources.Resource, error) {
	if e := valid(id); e != nil {
		return resources.Resource{}, e
	}
	i := index(d.Resources, func(v dataset.Resource) bool { return v.ID == id })
	if i < 0 {
		return resources.Resource{}, problem.NotFound()
	}
	r := d.Resources[i]
	if version == "" {
		version = r.CurrentVersionID
	}
	j := index(d.ResourceVersions, func(v dataset.ResourceVersion) bool { return v.ID == version && v.ResourceID == id })
	if j < 0 {
		return resources.Resource{}, problem.NotFound()
	}
	v := d.ResourceVersions[j]
	config, e := dataset.Value[resources.Config](v.Config)
	if e != nil {
		return resources.Resource{}, storageError()
	}
	resources.NormalizeConfig(&config)
	return resources.Resource{ID: id, EnvironmentID: r.EnvironmentID, Name: r.Name, Kind: r.Kind, VersionID: v.ID, Revision: v.Revision, Config: config, SecretNames: resources.SecretNames(v.Secrets), UpdatedAt: v.CreatedAt, ReadOnly: protected(d, id)}, nil
}
func (r *Resources) GetEnvironment(ctx context.Context, id string) (resources.Environment, error) {
	d, e := r.Store.data(ctx)
	if e != nil {
		return resources.Environment{}, e
	}
	return environment(&d, id)
}
func (r *Resources) GetResource(ctx context.Context, id string) (resources.Resource, error) {
	d, e := r.Store.data(ctx)
	if e != nil {
		return resources.Resource{}, e
	}
	return resource(&d, id, "")
}
func (r *Resources) ListEnvironments(ctx context.Context, limit int, cursor string) (catalog.Page[resources.Environment], error) {
	d, e := r.Store.data(ctx)
	if e != nil {
		return catalog.Page[resources.Environment]{}, e
	}
	items := []resources.Environment{}
	for _, v := range d.Environments {
		p, e := environment(&d, v.ID)
		if e != nil {
			return catalog.Page[resources.Environment]{}, e
		}
		items = append(items, p)
	}
	col := newNameOrder()
	sort.Slice(items, func(i, j int) bool { return alphabet(items[i].Name, items[j].Name, items[i].ID, items[j].ID, col) })
	return catalog.Paginate(items, limit, cursor, func(v resources.Environment) string { return v.ID })
}
func (r *Resources) ListResources(ctx context.Context, env string, limit int, cursor string) (catalog.Page[resources.Resource], error) {
	d, e := r.Store.data(ctx)
	if e != nil {
		return catalog.Page[resources.Resource]{}, e
	}
	if _, e = environment(&d, env); e != nil {
		return catalog.Page[resources.Resource]{}, e
	}
	items := []resources.Resource{}
	for _, v := range d.Resources {
		if v.EnvironmentID == env {
			p, e := resource(&d, v.ID, "")
			if e != nil {
				return catalog.Page[resources.Resource]{}, e
			}
			items = append(items, p)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		return items[i].ID < items[j].ID
	})
	return catalog.Paginate(items, limit, cursor, func(v resources.Resource) string { return v.ID })
}
func (r *Resources) SaveEnvironment(ctx context.Context, id, value string, revision int64, variables map[string]string, changes resources.SecretChanges) (resources.Environment, error) {
	return r.saveEnvironment(ctx, id, value, revision, variables, changes, nil)
}
func (r *Resources) SaveEnvironmentMetadata(ctx context.Context, id, name, description string, revision int64) (resources.Environment, error) {
	return r.saveEnvironment(ctx, id, name, revision, nil, resources.SecretChanges{}, &description)
}
func (r *Resources) SaveVariables(ctx context.Context, id string, revision int64, variables map[string]string, changes resources.SecretChanges) (resources.Environment, error) {
	old, e := r.GetEnvironment(ctx, id)
	if e != nil {
		return old, e
	}
	return r.SaveEnvironment(ctx, id, old.Name, revision, variables, changes)
}
func (r *Resources) saveEnvironment(ctx context.Context, id, value string, revision int64, variables map[string]string, changes resources.SecretChanges, description *string) (resources.Environment, error) {
	name, norm, e := catalog.Name(value)
	if e != nil {
		return resources.Environment{}, e
	}
	create := id == ""
	if create {
		id = catalog.NewID()
	}
	if variables == nil {
		variables = map[string]string{}
	}
	e = r.Store.Update(ctx, func(d *dataset.Data) error {
		if !create && protected(d, id) {
			return readOnly()
		}
		for _, v := range d.Environments {
			if v.ID != id && v.NormalizedName == norm {
				return problem.Invalid("name", "Такое имя уже занято, включая архив")
			}
		}
		i := index(d.Environments, func(v dataset.Environment) bool { return v.ID == id })
		oldSecrets := map[string]string{}
		if !create {
			if i < 0 {
				return problem.NotFound()
			}
			v := &d.Environments[i]
			if v.Revision != revision {
				return problem.Conflict("Окружение изменилось; обновите форму")
			}
			if description != nil {
				v.Name = name
				v.NormalizedName = norm
				v.Description = *description
				v.Revision++
				v.UpdatedAt = now()
				return nil
			}
			j := index(d.EnvironmentVersions, func(x dataset.EnvironmentVersion) bool { return x.ID == v.CurrentVersionID })
			oldSecrets = d.EnvironmentVersions[j].Secrets
		} else {
			t := now()
			desc := ""
			if description != nil {
				desc = *description
			}
			d.Environments = append(d.Environments, dataset.Environment{ID: id, Name: name, NormalizedName: norm, Description: desc, CreatedAt: t, UpdatedAt: t})
			i = len(d.Environments) - 1
		}
		values, e := resources.ApplySecretChanges(oldSecrets, changes)
		if e != nil {
			return e
		}
		if e = resources.ValidateVariables(variables, values); e != nil {
			return e
		}
		v := &d.Environments[i]
		v.Revision++
		v.Name = name
		v.NormalizedName = norm
		v.UpdatedAt = now()
		v.CurrentVersionID = catalog.NewID()
		d.EnvironmentVersions = append(d.EnvironmentVersions, dataset.EnvironmentVersion{ID: v.CurrentVersionID, EnvironmentID: id, Revision: v.Revision, Variables: variables, Secrets: values, SecretNames: resources.SecretNames(values), CreatedAt: now()})
		return nil
	})
	if e != nil {
		return resources.Environment{}, e
	}
	return r.GetEnvironment(ctx, id)
}
func (r *Resources) SaveResource(ctx context.Context, id, env, name, kind string, revision int64, config resources.Config, changes resources.SecretChanges) (resources.Resource, error) {
	create := id == ""
	if create {
		id = catalog.NewID()
	}
	resources.NormalizeConfig(&config)
	if config.TLS.Mode == "tls" && config.TLS.Certificate != "" {
		config.TLS.Mode = "mtls"
	}
	e := r.Store.Update(ctx, func(d *dataset.Data) error {
		if !create && protected(d, id) {
			return readOnly()
		}
		if !create {
			i := index(d.Resources, func(v dataset.Resource) bool { return v.ID == id })
			if i < 0 {
				return problem.NotFound()
			}
			env = d.Resources[i].EnvironmentID
		}
		en, e := environment(d, env)
		if e != nil {
			return e
		}
		if en.ReadOnly {
			return readOnly()
		}
		if e = resources.ValidateVariables(map[string]string{name: ""}, nil); e != nil {
			return e
		}
		if len(name) > 200 {
			return problem.Invalid("name", "Имя ресурса не больше 200 символов")
		}
		i := index(d.Resources, func(v dataset.Resource) bool { return v.ID == id })
		oldSecrets := map[string]string{}
		if !create {
			if i < 0 {
				return problem.NotFound()
			}
			old := d.Resources[i]
			if old.EnvironmentID != env || old.Kind != kind {
				return problem.Invalid("kind", "Протокол и окружение ресурса неизменяемы")
			}
			if old.Revision != revision {
				return problem.Conflict("Ресурс изменился; обновите форму")
			}
			j := index(d.ResourceVersions, func(v dataset.ResourceVersion) bool { return v.ID == old.CurrentVersionID })
			oldSecrets = d.ResourceVersions[j].Secrets
		}
		for _, v := range d.Resources {
			if v.ID != id && v.EnvironmentID == env && v.Name == name {
				return problem.Invalid("name", "Имя ресурса уже занято")
			}
		}
		secrets, e := resources.ApplySecretChanges(oldSecrets, changes)
		if e != nil {
			return e
		}
		sourceSecrets := map[string]string{}
		for _, v := range d.EnvironmentVersions {
			if v.EnvironmentID == env {
				for _, en := range d.Environments {
					if en.ID == env && en.CurrentVersionID == v.ID {
						for k, val := range v.Secrets {
							sourceSecrets[k] = val
						}
					}
				}
			}
		}
		for k, v := range secrets {
			sourceSecrets[k] = v
		}
		if e = resources.ValidateConfig(kind, config, secrets, sourceSecrets); e != nil {
			return e
		}
		if create {
			t := now()
			d.Resources = append(d.Resources, dataset.Resource{ID: id, EnvironmentID: env, Name: name, Kind: kind, CreatedAt: t, UpdatedAt: t})
			i = len(d.Resources) - 1
		}
		v := &d.Resources[i]
		v.Name = name
		v.Revision++
		v.UpdatedAt = now()
		v.CurrentVersionID = catalog.NewID()
		d.ResourceVersions = append(d.ResourceVersions, dataset.ResourceVersion{ID: v.CurrentVersionID, ResourceID: id, Revision: v.Revision, Config: dataset.Raw(config), Secrets: secrets, SecretNames: resources.SecretNames(secrets), CreatedAt: now()})
		return nil
	})
	if e != nil {
		return resources.Resource{}, e
	}
	return r.GetResource(ctx, id)
}
func (r *Resources) UploadProto(ctx context.Context, id string, revision int64, files map[string]string) (resources.Resource, error) {
	v, e := r.GetResource(ctx, id)
	if e != nil {
		return v, e
	}
	if v.Kind != "grpc" {
		return v, problem.Invalid("kind", "Proto-bundle относится к gRPC-ресурсу")
	}
	v.Config.ProtoFiles = files
	return r.SaveResource(ctx, id, v.EnvironmentID, v.Name, v.Kind, revision, v.Config, resources.SecretChanges{})
}
func (r *Resources) Freeze(ctx context.Context, env string, needs map[string]string) (resources.Snapshot, error) {
	d, e := r.Store.data(ctx)
	if e != nil {
		return resources.Snapshot{}, e
	}
	en, e := environment(&d, env)
	if e != nil {
		return resources.Snapshot{}, e
	}
	i := index(d.Environments, func(v dataset.Environment) bool { return v.ID == env })
	snapshot := resources.Snapshot{EnvironmentID: env, EnvironmentName: en.Name, EnvironmentVersionID: d.Environments[i].CurrentVersionID, ResourceVersions: map[string]string{}, Resources: []resources.Resource{}}
	names := []string{}
	for name := range needs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		i := index(d.Resources, func(v dataset.Resource) bool { return v.EnvironmentID == env && v.Name == name })
		if i < 0 {
			return resources.Snapshot{}, problem.Invalid("environmentId", "Ресурс "+name+" не найден в выбранном окружении")
		}
		v, e := resource(&d, d.Resources[i].ID, "")
		if e != nil {
			return resources.Snapshot{}, e
		}
		if v.Kind != needs[name] {
			return resources.Snapshot{}, problem.Invalid("resource", "Несовместимый протокол ресурса "+name)
		}
		snapshot.ResourceVersions[name] = v.VersionID
		snapshot.Resources = append(snapshot.Resources, v)
	}
	return snapshot, nil
}
func (r *Resources) Resolve(ctx context.Context, snapshot resources.Snapshot, name string) (resources.Resolved, error) {
	d, e := r.Store.data(ctx)
	if e != nil {
		return resources.Resolved{}, e
	}
	version, ok := snapshot.ResourceVersions[name]
	if !ok {
		return resources.Resolved{}, problem.Invalid("resource", "Ресурс отсутствует в снимке прогона")
	}
	var rv resources.Resource
	for _, v := range snapshot.Resources {
		if v.Name == name && v.VersionID == version {
			rv = v
			break
		}
	}
	if rv.ID == "" {
		return resources.Resolved{}, problem.Conflict("Повреждён снимок ресурса")
	}
	i := index(d.ResourceVersions, func(v dataset.ResourceVersion) bool { return v.ID == version && v.ResourceID == rv.ID })
	j := index(d.EnvironmentVersions, func(v dataset.EnvironmentVersion) bool {
		return v.ID == snapshot.EnvironmentVersionID && v.EnvironmentID == snapshot.EnvironmentID
	})
	if i < 0 || j < 0 {
		return resources.Resolved{}, problem.NotFound()
	}
	v, ev := d.ResourceVersions[i], d.EnvironmentVersions[j]
	config, e := dataset.Value[resources.Config](v.Config)
	if e != nil {
		return resources.Resolved{}, storageError()
	}
	rv.Config = config
	secrets := v.Secrets
	if secrets == nil {
		secrets = map[string]string{}
	}
	for k, val := range ev.Secrets {
		if _, ok := secrets[k]; ok {
			return resources.Resolved{}, problem.Invalid("secrets", "Имя секрета "+k+" повторяется в окружении и ресурсе")
		}
		secrets[k] = val
	}
	for k := range secrets {
		if _, ok := ev.Variables[k]; ok {
			return resources.Resolved{}, problem.Invalid("variables", "Имя "+k+" одновременно открытое и секретное")
		}
	}
	return resources.Resolved{Resource: rv, Variables: ev.Variables, Secrets: secrets}, nil
}
