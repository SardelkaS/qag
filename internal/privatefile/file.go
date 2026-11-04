// Package privatefile implements private, atomic local file publication.
package privatefile

import (
	"errors"
	"os"
	"path/filepath"
)

var ErrBusy = errors.New("файл уже используется другим процессом qag; остановите его перед переносом")

func Create(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	if err = restrict(f); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	return f, nil
}

// Atomic publishes a full private file. changed reports that publication may
// already have happened, even if durable confirmation subsequently failed.
func Atomic(path string, data []byte, newFile bool, fail func(string) error) (changed bool, err error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".qag-*.tmp")
	if err != nil {
		return false, err
	}
	temp := f.Name()
	defer os.Remove(temp)
	defer f.Close()
	if err = restrict(f); err != nil {
		return false, err
	}
	stage := func(s string) error {
		if fail != nil {
			return fail(s)
		}
		return nil
	}
	if err = stage("before-write"); err != nil {
		return false, err
	}
	if _, err = f.Write(data); err != nil {
		return false, err
	}
	if err = f.Sync(); err != nil {
		return false, err
	}
	if err = f.Close(); err != nil {
		return false, err
	}
	if err = stage("before-replace"); err != nil {
		return false, err
	}
	if err = publish(temp, path, newFile); err != nil {
		return false, err
	}
	if err = stage("after-replace"); err != nil {
		return true, err
	}
	if err = syncDir(filepath.Dir(path)); err != nil {
		return true, err
	}
	return true, nil
}

func Canonical(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return "", err
	}
	absolute = filepath.Join(dir, filepath.Base(absolute))
	if info, e := os.Lstat(absolute); e == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("нужен обычный файл данных без ссылок")
		}
		f, e := os.Open(absolute)
		if e != nil {
			return "", e
		}
		defer f.Close()
		if e = singleLink(f); e != nil {
			return "", e
		}
	} else if !os.IsNotExist(e) {
		return "", e
	}
	return absolute, nil
}

func Lock(path string) (*os.File, error) {
	f, err := Create(path)
	if os.IsExist(err) {
		f, err = os.OpenFile(path, os.O_RDWR, 0600)
	}
	if err != nil {
		return nil, err
	}
	if err = lock(f); err != nil {
		f.Close()
		return nil, ErrBusy
	}
	return f, nil
}
