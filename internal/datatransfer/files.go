package datatransfer

import (
	"context"
	"errors"
	"os"

	"github.com/SardelkaS/qag/internal/dataset"
	"github.com/SardelkaS/qag/internal/filestore"
	"github.com/SardelkaS/qag/internal/privatefile"
)

func Read(path string) (dataset.Document, error) {
	f, e := os.Open(path)
	if e != nil {
		return dataset.Document{}, errors.New("не удалось прочитать входной YAML")
	}
	defer f.Close()
	return dataset.Decode(f)
}
func Export(path string, doc dataset.Document) error {
	p, e := privatefile.Canonical(path)
	if e != nil {
		return errors.New("недоступен путь экспорта YAML")
	}
	lock, e := privatefile.Lock(p + ".lock")
	if e != nil {
		return e
	}
	defer lock.Close()
	if _, e = os.Lstat(p); !os.IsNotExist(e) {
		return errors.New("файл экспорта уже существует или недоступен; выберите новое имя")
	}
	b, e := dataset.Encode(doc)
	if e != nil {
		return e
	}
	_, e = privatefile.Atomic(p, b, true, nil)
	if e != nil {
		return errors.New("не удалось подтвердить запись экспорта YAML")
	}
	return nil
}
func ImportFile(ctx context.Context, store *filestore.Store, inputPath string, input dataset.Document, dry bool) (dataset.Summary, error) {
	p, e := privatefile.Canonical(inputPath)
	if e != nil {
		return nil, errors.New("недоступен входной YAML")
	}
	if p == store.Path() {
		return nil, errors.New("входной YAML и хранилище назначения должны различаться")
	}
	if in, e := os.Stat(p); e == nil {
		if target, e := os.Stat(store.Path()); e == nil && os.SameFile(in, target) {
			return nil, errors.New("входной YAML и хранилище назначения должны различаться")
		}
	}
	current, e := store.Snapshot(ctx)
	if e != nil {
		return nil, e
	}
	merged, summary, e := dataset.Merge(current, input)
	if e != nil || dry {
		return summary, e
	}
	if e = store.Replace(ctx, merged); e != nil {
		return summary, e
	}
	return summary, nil
}
