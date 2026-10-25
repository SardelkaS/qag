package resources

import "testing"

func TestInactiveStoredKeyDoesNotEnableMTLS(t *testing.T) {
	secrets := map[string]string{"PRIVATE_KEY": "stored-inactive-value"}
	for _, mode := range []string{"tls", "plaintext"} {
		cfg, err := TLS(Config{TLS: TLSConfig{Mode: mode}}, secrets)
		if err != nil {
			t.Fatal(err)
		}
		if mode == "plaintext" && cfg != nil || cfg != nil && len(cfg.Certificates) != 0 {
			t.Fatal("inactive private key enabled client authentication")
		}
		if secrets["PRIVATE_KEY"] != "stored-inactive-value" {
			t.Fatal("stored key was removed")
		}
	}
	if _, err := TLS(Config{TLS: TLSConfig{Mode: "mtls"}}, secrets); err == nil {
		t.Fatal("mTLS without client certificate accepted")
	}
}
