package testfixture

import (
	"bytes"
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalPKIIsStableAndRejectsMissingFiles(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureLocalPKI(dir); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err := EnsureLocalPKI(dir); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if !bytes.Equal(before, after) {
		t.Fatal("CA changed")
	}
	cert, err := IssueLocalClient(dir, "qag_test_client")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tls.X509KeyPair([]byte(cert.Client), []byte(cert.Key)); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(dir, "server.key")); err != nil {
		t.Fatal(err)
	}
	if EnsureLocalPKI(dir) == nil {
		t.Fatal("incomplete PKI regenerated")
	}
}
