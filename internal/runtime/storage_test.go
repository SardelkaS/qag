package runtime

import (
	"encoding/base64"
	"testing"
)

func TestStorageSelectionAndUnusedPostgres(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	for _, test := range []struct {
		name       string
		env        map[string]string
		mode, path string
		bad        bool
	}{
		{"default", map[string]string{}, "yaml", "./qag.yaml", false},
		{"file", map[string]string{"QAG_DATA_FILE": "given.yaml"}, "yaml", "given.yaml", false},
		{"explicit file ignores DB", map[string]string{"QAG_STORAGE": "yaml", "QAG_DATA_FILE": "given.yaml", "QAG_DATABASE_URL": "invalid", "QAG_SECRET_KEY": "invalid", "QAG_DEMO_DATABASE_URL": "invalid"}, "yaml", "given.yaml", false},
		{"ambiguous", map[string]string{"QAG_DATA_FILE": "given.yaml", "QAG_DATABASE_URL": "postgres://u@localhost/qag"}, "", "", true},
		{"invalid DB", map[string]string{"QAG_STORAGE": "postgres", "QAG_DATABASE_URL": "invalid"}, "", "", true},
		{"data commands no demo", map[string]string{"QAG_DATABASE_URL": "postgres://u@localhost/qag", "QAG_SECRET_KEY": key}, "postgres", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, e := LoadDataConfigEnv(func(k string) (string, bool) { v, ok := test.env[k]; return v, ok })
			if (e != nil) != test.bad {
				t.Fatal(e)
			}
			if !test.bad && (c.Storage != test.mode || c.DataFile != test.path) {
				t.Fatal(c.Storage, c.DataFile)
			}
		})
	}
}
