//go:build !windows

package privatefile

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func restrict(f *os.File) error { return f.Chmod(0600) }
func lock(f *os.File) error     { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func singleLink(f *os.File) error {
	var s unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &s); err != nil {
		return err
	}
	if s.Nlink != 1 {
		return errors.New("hardlink файла данных не поддерживается")
	}
	return nil
}
func publish(temp, path string, newFile bool) error {
	if newFile {
		return publishNew(temp, path)
	}
	return os.Rename(temp, path)
}
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
