package resources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/problem"
)

func NormalizeNewVariables(values map[string]string) map[string]string {
	out := map[string]string{}
	for name, value := range values {
		if strings.TrimSpace(name) != "" {
			out[name] = value
		}
	}
	return out
}
func NormalizeResourceVariables(values map[string]VariableDefinition) map[string]VariableDefinition {
	out := map[string]VariableDefinition{}
	for name, value := range values {
		if strings.TrimSpace(name) != "" {
			out[name] = value
		}
	}
	return out
}

// HTTPVariables distinguishes an omitted update (nil) from an explicit empty
// set. A null update is never an implicit delete.
type HTTPVariables map[string]catalog.HTTPHeaderSource

func (v *HTTPVariables) UnmarshalJSON(raw []byte) error {
	if strings.TrimSpace(string(raw)) == "null" {
		return fmt.Errorf("httpVariables: нужен объект, не null")
	}
	type plain HTTPVariables
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*v = HTTPVariables(value)
	return nil
}

func NormalizeHTTPVariables(values map[string]catalog.HTTPHeaderSource) map[string]catalog.HTTPHeaderSource {
	out := map[string]catalog.HTTPHeaderSource{}
	for name, source := range values {
		if strings.TrimSpace(name) != "" {
			out[name] = source
		}
	}
	return out
}

func EnvironmentVariableContext(variables map[string]string, sources map[string]catalog.HTTPHeaderSource, secrets map[string]string) error {
	if err := ValidateVariables(variables, secrets); err != nil {
		return err
	}
	res, err := (Resolved{Variables: variables, Secrets: secrets, EnvironmentVariables: variables, EnvironmentSecrets: secrets, EnvironmentHTTPVariables: sources}).withVariables()
	if err != nil {
		return err
	}
	for _, source := range sources {
		if _, err := res.EnvironmentSourceContext().PrepareVariableSource(source); err != nil {
			return err
		}
	}
	return nil
}

func CheckEnvironmentNames(config Config, secretNames []string, variables map[string]string, sources map[string]catalog.HTTPHeaderSource, secrets map[string]string) error {
	for name := range config.Variables {
		_, literal := variables[name]
		_, dynamic := sources[name]
		_, secret := secrets[name]
		if literal || dynamic || secret {
			return problem.Invalid("variables", "Имя "+name+" повторяется в окружении и ресурсе")
		}
	}
	for _, name := range secretNames {
		if _, exists := sources[name]; exists {
			return problem.Invalid("httpVariables", "Имя "+name+" одновременно HTTP-переменная и секрет ресурса")
		}
	}
	return nil
}

// Environment sources never inherit a target resource's credentials or locals.
func (res Resolved) EnvironmentSourceContext() Resolved {
	return Resolved{Variables: res.EnvironmentVariables, Secrets: res.EnvironmentSecrets, EnvironmentHTTPVariables: res.EnvironmentHTTPVariables, EnvironmentVariables: res.EnvironmentVariables, EnvironmentSecrets: res.EnvironmentSecrets}
}

func ValidateVariableDefinitions(config Config, secrets map[string]string, checkTLS bool) error {
	for name, def := range config.Variables {
		if !variableName.MatchString(name) {
			return problem.Invalid("config.variables", "Имена переменных должны быть в формате TASK_ID")
		}
		if _, ok := secrets[name]; ok {
			return problem.Invalid("config.variables", "Имя "+name+" одновременно переменная и секрет")
		}
		switch def.Mode {
		case "literal":
			if def.Source != nil {
				return problem.Invalid("config.variables", "Статическая переменная не имеет HTTP-источника")
			}
		case "http":
			if def.Source == nil || def.Value != "" {
				return problem.Invalid("config.variables", "HTTP-переменной нужен отдельный источник")
			}
			if err := catalog.ValidateHeaderSource(*def.Source); err != nil {
				return problem.Invalid("config.variables", err.Error())
			}
			if checkTLS {
				if _, err := HeaderTLS(*def.Source, secrets); err != nil {
					return err
				}
			}
		default:
			return problem.Invalid("config.variables", "Выберите literal или http")
		}
	}
	return nil
}

// VariableContext checks only configuration. It never fetches an HTTP value.
func VariableContext(config Config, variables, secrets map[string]string, sources ...map[string]catalog.HTTPHeaderSource) error {
	if err := ValidateVariableDefinitions(config, secrets, true); err != nil {
		return err
	}
	res := Resolved{Resource: Resource{Config: config}, Variables: variables, Secrets: secrets}
	if len(sources) > 0 {
		res.EnvironmentHTTPVariables = sources[0]
	}
	res, err := res.withVariables()
	if err != nil {
		return err
	}
	for name, def := range config.Variables {
		if def.Mode == "literal" {
			planned := res
			planned.planning = true
			if _, err := planned.Expand("${"+name+"}", true, nil); err != nil {
				return err
			}
		}
		if def.Mode == "http" {
			if _, err := res.PrepareVariableSource(*def.Source); err != nil {
				return err
			}
		}
	}
	for _, header := range config.Headers {
		if header.Source != nil {
			if _, err := res.PrepareVariableSource(*header.Source); err != nil {
				return err
			}
		}
	}
	return nil
}

func (res Resolved) withVariables() (Resolved, error) {
	if res.variablesReady {
		return res, nil
	}
	if err := ValidateVariableDefinitions(res.Resource.Config, res.Secrets, false); err != nil {
		return res, err
	}
	values := map[string]string{}
	if res.EnvironmentVariables == nil {
		res.EnvironmentVariables = res.Variables
	}
	for k, v := range res.Variables {
		values[k] = v
	}
	for name, source := range res.EnvironmentHTTPVariables {
		if !variableName.MatchString(name) {
			return res, problem.Invalid("httpVariables", "Имена переменных должны быть в формате TASK_ID")
		}
		if _, exists := values[name]; exists {
			return res, problem.Invalid("httpVariables", "Имя "+name+" повторяется в строковых и HTTP-переменных")
		}
		if _, exists := res.Secrets[name]; exists {
			return res, problem.Invalid("httpVariables", "Имя "+name+" одновременно переменная и секрет")
		}
		if err := catalog.ValidateHeaderSource(source); err != nil {
			return res, problem.Invalid("httpVariables", err.Error())
		}
	}
	for name, def := range res.Resource.Config.Variables {
		if _, exists := res.EnvironmentHTTPVariables[name]; exists {
			return res, problem.Invalid("config.variables", "Имя "+name+" повторяется в окружении и ресурсе")
		}
		if _, ok := values[name]; ok {
			return res, problem.Invalid("config.variables", "Имя "+name+" повторяется в окружении и ресурсе")
		}
		if _, ok := res.Secrets[name]; ok {
			return res, problem.Invalid("config.variables", "Имя "+name+" одновременно переменная и секрет")
		}
		if def.Mode == "literal" {
			values[name] = def.Value
		}
	}
	res.Variables = values
	res.variablesReady = true
	return res, nil
}

// PrepareVariableSource reuses the independent HTTP/TLS source contract, with
// dynamic-to-dynamic dependencies explicitly prohibited before any request.
func (res Resolved) PrepareVariableSource(source catalog.HTTPHeaderSource) (catalog.HTTPHeaderSource, error) {
	sourceBytes, _ := json.Marshal(source)
	var sourceCopy catalog.HTTPHeaderSource
	_ = json.Unmarshal(sourceBytes, &sourceCopy)
	source = sourceCopy
	res.forbidDynamic = true
	headers, err := res.prepareRequestHeaders([]catalog.RequestHeader{{ID: "00000000-0000-4000-8000-000000000001", Name: "X-Qag-Value", Source: &source}})
	if err != nil {
		return source, err
	}
	return *headers[0].Source, nil
}

func (res Resolved) RequiredVariables(block catalog.Block) ([]string, error) {
	values := []string{}
	collectJSON := func(raw string) {
		var walk func(any)
		walk = func(v any) {
			switch x := v.(type) {
			case string:
				values = append(values, x)
			case map[string]any:
				for _, child := range x {
					walk(child)
				}
			case []any:
				for _, child := range x {
					walk(child)
				}
			}
		}
		var v any
		if json.Unmarshal([]byte(raw), &v) == nil {
			walk(v)
		}
	}
	headers := []catalog.RequestHeader{}
	if b := block.HTTP; b != nil {
		values = append(values, b.Path)
		for _, p := range b.Query {
			values = append(values, p.Value)
		}
		if b.BodyMode == "json" {
			collectJSON(b.Body)
		} else {
			values = append(values, b.Body)
		}
		headers = catalog.MergeRequestHeaders(res.Resource.Config.Headers, b.Headers)
	}
	if b := block.GRPC; b != nil {
		collectJSON(b.Body)
		headers = catalog.MergeRequestHeaders(res.Resource.Config.Headers, b.Metadata)
	}
	if b := block.SQL; b != nil {
		for _, p := range b.Parameters {
			if p.Type == "json" {
				collectJSON(p.Value)
			} else {
				values = append(values, p.Value)
			}
		}
	}
	for _, h := range headers {
		if h.Source == nil {
			values = append(values, h.Value)
		} else {
			// An existing header source is independent too: its transport never
			// recursively inherits the target's headers or HTTP values.
			strict := res
			strict.forbidDynamic = true
			if _, err := strict.PrepareVariableSource(*h.Source); err != nil {
				return nil, err
			}
		}
	}
	required, visiting := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(text string) error {
		if strings.Contains(substitution.ReplaceAllString(text, ""), "${") {
			return problem.Invalid("variables", "Некорректная подстановка")
		}
		for _, token := range substitution.FindAllStringSubmatch(text, -1) {
			name := token[1]
			if _, ok := res.DynamicVariable(name); ok {
				required[name] = true
				continue
			}
			if _, ok := res.Secrets[name]; ok {
				continue
			}
			value, ok := res.Variables[name]
			if !ok {
				return problem.Invalid("variables", "Неизвестная переменная "+name)
			}
			if visiting[name] {
				return problem.Invalid("variables", "Циклическая переменная "+name)
			}
			visiting[name] = true
			if err := visit(value); err != nil {
				return err
			}
			delete(visiting, name)
		}
		return nil
	}
	for _, v := range values {
		if err := visit(v); err != nil {
			return nil, err
		}
	}
	names := []string{}
	for name := range required {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func (res Resolved) Plan(block catalog.Block) (catalog.Block, Resolved, error) {
	return res.plan(block, false)
}
func (res Resolved) PlanRequest(block catalog.Block) (catalog.Block, Resolved, error) {
	block.Expected = catalog.Expectation{}
	return res.plan(block, true)
}
func (res Resolved) plan(block catalog.Block, research bool) (catalog.Block, Resolved, error) {
	res, err := res.withVariables()
	if err != nil {
		return block, res, err
	}
	unsupported := []string{res.Resource.Config.BaseURL, res.Resource.Config.Address, res.Resource.Config.Database, res.Resource.Config.Username, res.Resource.Config.TLS.ServerName}
	if h := block.HTTP; h != nil {
		unsupported = append(unsupported, h.Method)
		for _, v := range h.Query {
			unsupported = append(unsupported, v.Name)
		}
		for _, v := range h.Headers {
			unsupported = append(unsupported, v.Name)
		}
	}
	if g := block.GRPC; g != nil {
		unsupported = append(unsupported, g.Service, g.Method)
		for _, v := range g.Metadata {
			unsupported = append(unsupported, v.Name)
		}
	}
	if s := block.SQL; s != nil {
		unsupported = append(unsupported, s.Query)
	}
	if !research {
		raw, _ := json.Marshal(block.Expected)
		unsupported = append(unsupported, string(raw))
	}
	var check func(string, map[string]bool) error
	check = func(value string, seen map[string]bool) error {
		for _, match := range substitution.FindAllStringSubmatch(value, -1) {
			name := match[1]
			if _, dynamic := res.DynamicVariable(name); dynamic {
				return problem.Invalid("variables", "HTTP-переменная используется в неподдерживаемом поле или ожидании")
			}
			if nested, ok := res.Variables[name]; ok && !seen[name] {
				seen[name] = true
				if err := check(nested, seen); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, value := range unsupported {
		if err := check(value, map[string]bool{}); err != nil {
			return block, res, err
		}
	}
	for _, def := range res.Resource.Config.Variables {
		if def.Mode == "http" {
			if _, err = res.PrepareVariableSource(*def.Source); err != nil {
				return block, res, err
			}
		}
	}
	for _, source := range res.EnvironmentHTTPVariables {
		if _, err = res.EnvironmentSourceContext().PrepareVariableSource(source); err != nil {
			return block, res, err
		}
	}
	needed, err := res.RequiredVariables(block)
	if err != nil {
		return block, res, err
	}
	if len(needed) == 0 {
		return res.prepare(block, research)
	}
	planned := res
	planned.planning = true
	if _, _, err = planned.prepare(block, research); err != nil {
		return block, res, err
	}
	// Keep the original templates, rather than URL-encoding a placeholder or
	// replacing a decimal parameter with a made-up value in the stored request.
	res.Deferred = true
	return block, res, nil
}

func (res Resolved) DynamicVariable(name string) (VariableDefinition, bool) {
	if source, ok := res.EnvironmentHTTPVariables[name]; ok {
		return VariableDefinition{Mode: "http", Source: &source}, true
	}
	d, ok := res.Resource.Config.Variables[name]
	return d, ok && d.Mode == "http"
}

func (res Resolved) VariableOwner(name string) (scope, version string) {
	if _, ok := res.EnvironmentHTTPVariables[name]; ok {
		return "environment", res.EnvironmentVersionID
	}
	return "resource", res.Resource.VersionID
}

func (res Resolved) PrepareNamedVariableSource(name string) (catalog.HTTPHeaderSource, Resolved, error) {
	def, ok := res.DynamicVariable(name)
	if !ok || def.Source == nil {
		return catalog.HTTPHeaderSource{}, res, VariableError(name)
	}
	context := res
	if scope, _ := res.VariableOwner(name); scope == "environment" {
		context = res.EnvironmentSourceContext()
	}
	source, err := context.PrepareVariableSource(*def.Source)
	return source, context, err
}
func (res Resolved) WithDynamicValues(values map[string]string) Resolved {
	res.DynamicValues = values
	res.Deferred = false
	res.planning = false
	return res
}

func VariableError(name string) error {
	return fmt.Errorf("Не удалось получить HTTP-переменную %s", name)
}
