package resources

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestVaultAuthenticationAndRedaction(t *testing.T) {
	vault, err := NewVault(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"TOKEN": "sentinel-secret-123", "EMPTY": ""}
	sealed, err := vault.Seal("version-1", values)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte(values["TOKEN"])) {
		t.Fatal("plaintext persisted")
	}
	opened, err := vault.Open("version-1", sealed)
	if err != nil || opened["TOKEN"] != values["TOKEN"] {
		t.Fatal("cannot decrypt")
	}
	if _, exists := opened["EMPTY"]; !exists {
		t.Fatal("empty secret lost")
	}
	if _, err = vault.Open("other-version", sealed); err == nil {
		t.Fatal("associated data not authenticated")
	}
	other, _ := NewVault(bytes.Repeat([]byte{8}, 32))
	if _, err = other.Open("version-1", sealed); err == nil {
		t.Fatal("wrong key accepted")
	}
	redactor := NewRedactor(values)
	if redactor.Mask("token="+values["TOKEN"]) != "token=[скрыто]" {
		t.Fatal("secret leaked")
	}
	merged, err := mergeSecrets(opened, SecretChanges{Clear: []string{"TOKEN"}, Replace: map[string]string{"EMPTY": ""}})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := merged["TOKEN"]; exists {
		t.Fatal("clear ignored")
	}
	if _, exists := merged["EMPTY"]; !exists {
		t.Fatal("replace-empty ignored")
	}
}

func TestRedactEscapedAndNestedJSON(t *testing.T) {
	secret := "a\"b\n<&c"
	r := NewRedactor(map[string]string{"TOKEN": secret})
	inner, _ := json.Marshal(map[string]any{"echo": secret, "n": json.Number("9007199254740993123")})
	outer, _ := json.Marshal(map[string]any{"body": string(inner), "header": secret})
	masked := r.Mask(string(outer))
	if !json.Valid([]byte(masked)) || strings.Contains(masked, `a\"b`) {
		t.Fatal("escaped secret leaked or JSON corrupted")
	}
	var decoded map[string]string
	if json.Unmarshal([]byte(masked), &decoded) != nil || decoded["header"] != "[скрыто]" || !strings.Contains(decoded["body"], "9007199254740993123") || strings.Contains(decoded["body"], secret) {
		t.Fatal("nested redaction or numeric precision lost")
	}
	if got := r.Mask(`{"x":"\u0061\"b\n\u003c\u0026c"}`); !strings.Contains(got, "[скрыто]") {
		t.Fatal("Unicode escape leaked")
	}
}
