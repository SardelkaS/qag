package resources

import (
	"bytes"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/problem"
)

var substitution = regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]*)\}`)

func (res Resolved) Expand(value string, secretAllowed bool, escape func(string) string) (string, error) {
	visiting := map[string]bool{}
	var failure error
	var expand func(string) string
	expand = func(input string) string {
		return substitution.ReplaceAllStringFunc(input, func(token string) string {
			name := token[2 : len(token)-1]
			if visiting[name] {
				failure = problem.Invalid("variables", "Циклическая переменная "+name)
				return token
			}
			if secret, ok := res.Secrets[name]; ok {
				if !secretAllowed {
					failure = problem.Invalid("variables", "Секрет "+name+" допустим только в заголовках, метаданных и подключении")
					return token
				}
				if escape != nil {
					return escape(secret)
				}
				return secret
			}
			value, ok := res.Variables[name]
			if !ok {
				failure = problem.Invalid("variables", "Неизвестная переменная "+name)
				return token
			}
			visiting[name] = true
			value = expand(value)
			delete(visiting, name)
			if escape != nil {
				return escape(value)
			}
			return value
		})
	}
	// Nested values are expanded before the outer field's URL encoding.
	if escape != nil {
		plain, err := res.Expand(value, secretAllowed, nil)
		if err != nil {
			return "", err
		}
		return substitution.ReplaceAllStringFunc(value, func(token string) string {
			resolved, _ := res.Expand(token, secretAllowed, nil)
			return escape(resolved)
		}), checkExpansion(plain, nil)
	}
	result := expand(value)
	return result, checkExpansion(result, failure)
}
func checkExpansion(value string, err error) error {
	if err != nil {
		return err
	}
	if strings.Contains(value, "${") {
		return problem.Invalid("variables", "Некорректная или неизвестная подстановка")
	}
	return nil
}

func (res Resolved) Prepare(original catalog.Block) (catalog.Block, Resolved, error) {
	return res.prepare(original, false)
}

func (res Resolved) PrepareRequest(original catalog.Block) (catalog.Block, Resolved, error) {
	original.Expected = catalog.Expectation{}
	return res.prepare(original, true)
}

func (res Resolved) prepare(original catalog.Block, research bool) (catalog.Block, Resolved, error) {
	data, _ := json.Marshal(original)
	var b catalog.Block
	_ = json.Unmarshal(data, &b)
	resourceData, _ := json.Marshal(res.Resource)
	var resourceCopy Resource
	_ = json.Unmarshal(resourceData, &resourceCopy)
	res.Resource = resourceCopy
	fail := func(err error) (catalog.Block, Resolved, error) { return b, res, err }
	var err error
	if b.HTTP != nil {
		b.HTTP.Headers, err = res.prepareRequestHeaders(catalog.MergeRequestHeaders(res.Resource.Config.Headers, b.HTTP.Headers))
		if err != nil {
			return fail(err)
		}
	}
	if b.GRPC != nil {
		b.GRPC.Metadata, err = res.prepareRequestHeaders(catalog.MergeRequestHeaders(res.Resource.Config.Headers, b.GRPC.Metadata))
		if err != nil {
			return fail(err)
		}
	}
	res.Resource.Config.Headers = []catalog.RequestHeader{}
	if b.HTTP != nil {
		b.HTTP.Path = strings.TrimPrefix(b.HTTP.Path, b.Resource+"/")
		b.HTTP.Path, err = res.Expand(b.HTTP.Path, false, url.PathEscape)
		if err != nil {
			return fail(err)
		}
		for i := range b.HTTP.Query {
			b.HTTP.Query[i].Value, err = res.Expand(b.HTTP.Query[i].Value, false, nil)
			if err != nil {
				return fail(err)
			}
		}
		if b.HTTP.BodyMode == "json" {
			b.HTTP.Body, err = res.expandJSON(b.HTTP.Body)
		} else {
			b.HTTP.Body, err = res.Expand(b.HTTP.Body, false, nil)
		}
		if err != nil {
			return fail(err)
		}
	}
	if b.GRPC != nil {
		b.GRPC.Body, err = res.expandJSON(b.GRPC.Body)
		if err != nil {
			return fail(err)
		}
	}
	if b.SQL != nil {
		for i := range b.SQL.Parameters {
			value := &b.SQL.Parameters[i]
			if value.Type == "json" {
				value.Value, err = res.expandJSON(value.Value)
			} else {
				value.Value, err = res.Expand(value.Value, false, nil)
			}
			if err != nil {
				return fail(err)
			}
			if err = catalog.ValidateValue(*value); err != nil {
				return fail(problem.Invalid("sql.parameters", err.Error()))
			}
		}
	}
	if research {
		if err = catalog.ValidateRequest(&b, "block"); err != nil {
			return fail(err)
		}
		return b, res, nil
	}
	if b.Expected.BodyMode == "json" {
		b.Expected.Body, err = res.expandJSON(b.Expected.Body)
	} else {
		b.Expected.Body, err = res.Expand(b.Expected.Body, false, nil)
	}
	if err != nil {
		return fail(err)
	}
	if err = catalog.ValidateBlock(&b, "block"); err != nil {
		return fail(err)
	}
	return b, res, nil
}

func (res Resolved) expandJSON(text string) (string, error) {
	return res.expandJSONAllowed(text, false)
}
func (res Resolved) expandJSONAllowed(text string, secretAllowed bool) (string, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(text))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", problem.Invalid("body", "Некорректный JSON")
	}
	var failure error
	var visit func(any) any
	visit = func(input any) any {
		switch v := input.(type) {
		case string:
			result, err := res.Expand(v, secretAllowed, nil)
			if err != nil {
				failure = err
			}
			return result
		case map[string]any:
			for k, child := range v {
				v[k] = visit(child)
			}
		case []any:
			for i, child := range v {
				v[i] = visit(child)
			}
		}
		return input
	}
	value = visit(value)
	if failure != nil {
		return "", failure
	}
	out, err := json.Marshal(value)
	return string(out), err
}
