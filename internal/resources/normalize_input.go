package resources

import (
	"encoding/json"
	"strings"
)

// NormalizeVariableWriteJSON removes unnamed new rows before wire validation.
// RawMessage keeps authored JSON strings and exact numeric tokens intact.
func NormalizeVariableWriteJSON(raw []byte) []byte {
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil || root == nil {
		return raw
	}
	cleanMap := func(object map[string]json.RawMessage, key string) bool {
		var values map[string]json.RawMessage
		if json.Unmarshal(object[key], &values) != nil || values == nil {
			return false
		}
		changed := false
		for name := range values {
			if strings.TrimSpace(name) == "" {
				delete(values, name)
				changed = true
			}
		}
		if changed {
			object[key], _ = json.Marshal(values)
		}
		return changed
	}
	changed := cleanMap(root, "variables")
	var config map[string]json.RawMessage
	if json.Unmarshal(root["config"], &config) == nil && cleanMap(config, "variables") {
		root["config"], _ = json.Marshal(config)
		changed = true
	}
	var secrets map[string]json.RawMessage
	if json.Unmarshal(root["secrets"], &secrets) == nil && secrets != nil {
		secretChanged := cleanMap(secrets, "replace")
		var clear []json.RawMessage
		if json.Unmarshal(secrets["clear"], &clear) == nil && clear != nil {
			kept := make([]json.RawMessage, 0, len(clear))
			for _, value := range clear {
				var name string
				if json.Unmarshal(value, &name) == nil && strings.TrimSpace(name) == "" {
					secretChanged = true
					continue
				}
				kept = append(kept, value)
			}
			if len(kept) != len(clear) {
				secrets["clear"], _ = json.Marshal(kept)
			}
		}
		if secretChanged {
			root["secrets"], _ = json.Marshal(secrets)
			changed = true
		}
	}
	if !changed {
		return raw
	}
	result, err := json.Marshal(root)
	if err != nil {
		return raw
	}
	return result
}
