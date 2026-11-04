package privatefile

import "golang.org/x/sys/unix"

func publishNew(temp, path string) error { return unix.RenamexNp(temp, path, unix.RENAME_EXCL) }
