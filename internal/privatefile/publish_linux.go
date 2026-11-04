package privatefile

import "golang.org/x/sys/unix"

func publishNew(temp, path string) error {
	return unix.Renameat2(unix.AT_FDCWD, temp, unix.AT_FDCWD, path, unix.RENAME_NOREPLACE)
}
