//go:build !windows && !linux && !darwin

package privatefile

import "errors"

func publishNew(string, string) error {
	return errors.New("атомарная публикация поддерживается на Windows, Linux и macOS")
}
