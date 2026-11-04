package resources

// ApplySecretChanges keeps absent fields, supports explicit clear, and never
// mutates the map retained by an older version.
func ApplySecretChanges(old map[string]string, changes SecretChanges) (map[string]string, error) {
	return mergeSecrets(old, changes)
}

func SecretNames(values map[string]string) []string { return secretNames(values) }

func NormalizeConfig(config *Config) { normalizeConfig(config) }
