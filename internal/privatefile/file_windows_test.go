package privatefile

import (
	"golang.org/x/sys/windows"
	"path/filepath"
	"testing"
)

func TestPrivateWindowsACL(t *testing.T) {
	p := filepath.Join(t.TempDir(), "private.yaml")
	f, e := Create(p)
	if e != nil {
		t.Fatal(e)
	}
	f.Close()
	sd, e := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		t.Fatal(e)
	}
	acl, _, e := sd.DACL()
	if e != nil || acl.AceCount != 1 {
		t.Fatal("private ACL", e)
	}
	control, _, e := sd.Control()
	if e != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("inherited ACL not disabled", e)
	}
}
