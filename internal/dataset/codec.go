package dataset

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

func invalid(group, id, reason string) error {
	// Only schema labels and validated identifiers are included, never input values.
	if len(id) > 80 {
		id = ""
	}
	return fmt.Errorf("некорректные данные qag: %s %s (%s)", group, id, reason)
}

func Decode(r io.Reader) (Document, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil || len(b) > MaxBytes || !utf8.Valid(b) {
		return Document{}, invalid("YAML", "", "размер или кодировка")
	}
	d := yaml.NewDecoder(bytes.NewReader(b))
	var n yaml.Node
	if d.Decode(&n) != nil || checkNode(&n, 0) != nil {
		return Document{}, invalid("YAML", "", "структура документа")
	}
	var extra yaml.Node
	if d.Decode(&extra) != io.EOF {
		return Document{}, invalid("YAML", "", "нужен один документ")
	}
	strict := yaml.NewDecoder(bytes.NewReader(b))
	strict.KnownFields(true)
	var doc Document
	if strict.Decode(&doc) != nil {
		return Document{}, invalid("YAML", "", "поля или типы")
	}
	if err = Validate(doc); err != nil {
		return Document{}, err
	}
	Canonicalize(&doc)
	return doc, nil
}

func checkNode(n *yaml.Node, depth int) error {
	if depth > MaxDepth || n.Kind == yaml.AliasNode || n.Anchor != "" {
		return invalid("YAML", "", "глубина или alias")
	}
	if n.Tag != "" && !strings.HasPrefix(n.Tag, "!!") {
		return invalid("YAML", "", "custom tag")
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || seen[k.Value] || k.Tag == "!!merge" {
				return invalid("YAML", "", "ключи")
			}
			seen[k.Value] = true
		}
	}
	for _, child := range n.Content {
		if err := checkNode(child, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func Encode(doc Document) ([]byte, error) {
	if err := Validate(doc); err != nil {
		return nil, err
	}
	copy, err := Clone(doc)
	if err != nil {
		return nil, err
	}
	Canonicalize(&copy)
	var buf bytes.Buffer
	e := yaml.NewEncoder(&buf)
	e.SetIndent(2)
	if err = e.Encode(copy); err != nil {
		return nil, invalid("YAML", "", "кодирование")
	}
	if buf.Len() > MaxBytes {
		return nil, invalid("YAML", "", "превышен размер 256 MiB")
	}
	return buf.Bytes(), nil
}

func Clone(doc Document) (Document, error) {
	b, err := json.Marshal(doc)
	if err != nil {
		return Document{}, invalid("данные", "", "копирование")
	}
	var copy Document
	err = json.Unmarshal(b, &copy)
	return copy, err
}

func key(v reflect.Value) string {
	if f := v.FieldByName("ID"); f.IsValid() {
		return f.String()
	}
	if f := v.FieldByName("Key"); f.IsValid() {
		return f.String()
	}
	parts := []string{}
	for _, name := range []string{"RunID", "StepID", "ResourceName", "BatchID", "Position"} {
		if f := v.FieldByName(name); f.IsValid() {
			parts = append(parts, fmt.Sprint(f.Interface()))
		}
	}
	return strings.Join(parts, "/")
}

func RecordKey(value any) string { return key(reflect.ValueOf(value)) }

func Canonicalize(doc *Document) {
	doc.Version = Version
	for i := range doc.Data.Runs {
		if doc.Data.Runs[i].HistoryPolicy == "" {
			doc.Data.Runs[i].HistoryPolicy = "auto"
		}
	}
	v := reflect.ValueOf(&doc.Data).Elem()
	for i := 0; i < v.NumField(); i++ {
		s := v.Field(i)
		if s.IsNil() {
			s.Set(reflect.MakeSlice(s.Type(), 0, 0))
		}
		sort.Slice(s.Interface(), func(a, b int) bool { return key(s.Index(a)) < key(s.Index(b)) })
		for j := 0; j < s.Len(); j++ {
			row := s.Index(j)
			for k := 0; k < row.NumField(); k++ {
				f := row.Field(k)
				if f.Type() == reflect.TypeFor[time.Time]() {
					f.Set(reflect.ValueOf(f.Interface().(time.Time).UTC()))
				}
				if f.Type() == reflect.TypeFor[*time.Time]() && !f.IsNil() {
					t := f.Interface().(*time.Time).UTC()
					f.Set(reflect.ValueOf(&t))
				}
			}
		}
	}
	for i := range doc.Data.EnvironmentVersions {
		v := &doc.Data.EnvironmentVersions[i]
		if v.Variables == nil {
			v.Variables = map[string]string{}
		}
		if v.Secrets == nil {
			v.Secrets = map[string]string{}
		}
		if v.SecretNames == nil {
			v.SecretNames = []string{}
		}
		sort.Strings(doc.Data.EnvironmentVersions[i].SecretNames)
	}
	for i := range doc.Data.ResourceVersions {
		v := &doc.Data.ResourceVersions[i]
		if v.Secrets == nil {
			v.Secrets = map[string]string{}
		}
		if v.SecretNames == nil {
			v.SecretNames = []string{}
		}
		sort.Strings(doc.Data.ResourceVersions[i].SecretNames)
	}
	for i := range doc.Data.Runs {
		if doc.Data.Runs[i].RetentionReasons == nil {
			doc.Data.Runs[i].RetentionReasons = []string{}
		}
		sort.Strings(doc.Data.Runs[i].RetentionReasons)
	}
}
