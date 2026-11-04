//go:build !windows

package privatefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivatePOSIXMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "private.yaml")
	f, e := Create(p)
	if e != nil {
		t.Fatal(e)
	}
	f.Close()
	info, e := os.Stat(p)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private mode", e)
	}
}
