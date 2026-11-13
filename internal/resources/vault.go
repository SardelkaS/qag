package resources

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/SardelkaS/qag/internal/problem"
)

type Vault struct{ aead cipher.AEAD }

func NewVault(key []byte) (*Vault, error) {
	if len(key) != 32 {
		return nil, errors.New("нужен 32-байтовый ключ секретов")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead}, nil
}
func (v *Vault) Seal(id string, values map[string]string) ([]byte, error) {
	plain, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, v.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return v.aead.Seal(nonce, nonce, plain, []byte("qag:"+id)), nil
}
func (v *Vault) Open(id string, data []byte) (map[string]string, error) {
	failed := func() (map[string]string, error) {
		return nil, problem.Conflict("Не удалось расшифровать секреты: проверьте ключ QAG_SECRET_KEY")
	}
	size := v.aead.NonceSize()
	if len(data) < size {
		return failed()
	}
	plain, err := v.aead.Open(nil, data[:size], data[size:], []byte("qag:"+id))
	if err != nil {
		return failed()
	}
	values := map[string]string{}
	if json.Unmarshal(plain, &values) != nil {
		return failed()
	}
	return values, nil
}
func mergeSecrets(old map[string]string, changes SecretChanges) (map[string]string, error) {
	result := map[string]string{}
	for k, v := range old {
		result[k] = v
	}
	for _, name := range changes.Clear {
		if strings.TrimSpace(name) == "" {
			continue
		}
		if !variableName.MatchString(name) {
			return nil, problem.Invalid("secrets", "Имена секретов должны быть в формате TOKEN_NAME")
		}
		delete(result, name)
	}
	for name, value := range changes.Replace {
		if strings.TrimSpace(name) == "" {
			continue
		}
		if !variableName.MatchString(name) {
			return nil, problem.Invalid("secrets", "Имена секретов должны быть в формате TOKEN_NAME")
		}
		result[name] = value
	}
	return result, nil
}
func secretNames(values map[string]string) []string {
	result := []string{}
	for k := range values {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}

// Redactor replaces only known plaintext secrets; it never persists a raw
// response to discover them later. Longer matches take precedence.
type Redactor struct{ replacer *strings.Replacer }

func NewRedactor(values map[string]string) *Redactor {
	set := map[string]bool{}
	for _, v := range values {
		if v != "" {
			set[v] = true
			set[url.QueryEscape(v)] = true
			set[url.PathEscape(v)] = true
			encoded, _ := json.Marshal(v)
			set[string(encoded[1:len(encoded)-1])] = true
		}
	}
	keys := []string{}
	for k := range set {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	pairs := []string{}
	for _, key := range keys {
		pairs = append(pairs, key, "[скрыто]")
	}
	return &Redactor{strings.NewReplacer(pairs...)}
}
func (r *Redactor) Mask(value string) string {
	// Decode JSON before replacing: a response can echo quotes, newlines or
	// Unicode escapes, and a request snapshot can itself contain JSON strings.
	if !json.Valid([]byte(value)) {
		return r.replacer.Replace(value)
	}
	decoder := json.NewDecoder(bytes.NewBufferString(value))
	decoder.UseNumber()
	var document any
	if decoder.Decode(&document) != nil {
		return r.replacer.Replace(value)
	}
	changed := false
	var visit func(any) any
	visit = func(input any) any {
		switch v := input.(type) {
		case string:
			masked := r.Mask(v)
			changed = changed || masked != v
			return masked
		case json.Number:
			masked := r.replacer.Replace(string(v))
			if masked != string(v) {
				changed = true
				return masked
			}
		case bool:
			original := strconv.FormatBool(v)
			if masked := r.replacer.Replace(original); masked != original {
				changed = true
				return masked
			}
		case map[string]any:
			out := make(map[string]any, len(v))
			for key, child := range v {
				masked := r.replacer.Replace(key)
				changed = changed || masked != key
				out[masked] = visit(child)
			}
			return out
		case []any:
			for i := range v {
				v[i] = visit(v[i])
			}
		}
		return input
	}
	document = visit(document)
	if !changed {
		return value
	}
	masked, _ := json.Marshal(document)
	return string(masked)
}
