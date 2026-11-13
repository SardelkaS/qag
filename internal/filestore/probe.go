package filestore

import (
	"context"

	"github.com/SardelkaS/qag/internal/dataset"
	"github.com/SardelkaS/qag/internal/problem"
	"github.com/SardelkaS/qag/internal/resources"
)

func (r *Resources) ResolveProbe(ctx context.Context, envID, resourceID string) (resources.Resolved, error) {
	var out resources.Resolved
	d, err := r.Store.data(ctx)
	if err != nil {
		return out, err
	}
	env, err := environment(&d, envID)
	if err != nil {
		return out, err
	}
	i := index(d.Environments, func(v dataset.Environment) bool { return v.ID == envID })
	version := d.Environments[i].CurrentVersionID
	i = index(d.EnvironmentVersions, func(v dataset.EnvironmentVersion) bool { return v.ID == version })
	if i < 0 {
		return out, storageError()
	}
	ev := d.EnvironmentVersions[i]
	out = resources.Resolved{EnvironmentID: envID, EnvironmentName: env.Name, EnvironmentRevision: env.Revision, EnvironmentVersionID: version, EnvironmentVariables: ev.Variables, EnvironmentHTTPVariables: ev.HTTPVariables, EnvironmentSecrets: ev.Secrets, Variables: ev.Variables, Secrets: map[string]string{}}
	for k, v := range ev.Secrets {
		out.Secrets[k] = v
	}
	if resourceID != "" {
		out.Resource, err = resource(&d, resourceID, "")
		if err != nil {
			return out, err
		}
		if out.Resource.EnvironmentID != envID {
			return out, problem.NotFound()
		}
		i = index(d.ResourceVersions, func(v dataset.ResourceVersion) bool { return v.ID == out.Resource.VersionID })
		if i < 0 {
			return out, storageError()
		}
		for k, v := range d.ResourceVersions[i].Secrets {
			if _, exists := out.Secrets[k]; exists {
				return out, problem.Invalid("secrets", "Имя секрета повторяется в окружении и ресурсе")
			}
			out.Secrets[k] = v
		}
	}
	return out, nil
}
