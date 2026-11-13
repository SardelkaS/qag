package dataset

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
)

type Counts struct {
	Added   int `json:"added"`
	Skipped int `json:"skipped"`
}
type Conflict struct {
	Collection string `json:"collection"`
	ID         string `json:"id"`
	Reason     string `json:"reason"`
}
type Conflicts struct{ Items []Conflict }

func (c *Conflicts) Error() string {
	return fmt.Sprintf("импорт отклонён: конфликтующих записей %d; существующие данные сохранены", len(c.Items))
}

type Summary map[string]Counts

func equivalent(a, b reflect.Value) bool {
	// Each row is a separate value; normalizing raw JSON for comparison never
	// rewrites the historical bytes stored in either document.
	canon := func(v reflect.Value) []byte {
		copy := reflect.New(v.Type()).Elem()
		copy.Set(v)
		for i := 0; i < copy.NumField(); i++ {
			f := copy.Field(i)
			if f.Type() == reflect.TypeFor[JSON]() && f.String() != "" {
				d := json.NewDecoder(bytes.NewBufferString(f.String()))
				d.UseNumber()
				var data any
				if d.Decode(&data) == nil {
					fieldName := copy.Type().Field(i).Name
					if copy.Type() == reflect.TypeFor[TestCase]() && fieldName == "Steps" {
						compareStepsDefaults(data)
					}
					if root, ok := data.(map[string]any); ok {
						if copy.Type() == reflect.TypeFor[Run]() && fieldName == "Snapshot" {
							compareStepsDefaults(root["steps"])
						}
						if copy.Type() == reflect.TypeFor[CallAttempt]() && fieldName == "SafeRequest" || copy.Type() == reflect.TypeFor[VariantVersion]() && fieldName == "Request" {
							compareRequestDefaults(root)
						}
						// Defaults are compared without rewriting any stored JSON.
						if copy.Type() == reflect.TypeFor[ResourceVersion]() && copy.Type().Field(i).Name == "Config" {
							compareResourceDefaults(root)
						}
						if copy.Type() == reflect.TypeFor[Run]() && copy.Type().Field(i).Name == "Context" {
							if list, ok := root["resources"].([]any); ok {
								for _, item := range list {
									if resource, ok := item.(map[string]any); ok {
										if config, ok := resource["config"].(map[string]any); ok {
											compareResourceDefaults(config)
										}
									}
								}
							}
						}
					}
					b, _ := json.Marshal(data)
					f.SetString(string(b))
				}
			}
		}
		b, _ := json.Marshal(copy.Interface())
		return b
	}
	return bytes.Equal(canon(a), canon(b))
}

func compareResourceDefaults(config map[string]any) {
	compareHeaderDefaults(config["headers"])
	value, exists := config["variables"]
	if variables, ok := value.(map[string]any); ok {
		for _, definition := range variables {
			if def, ok := definition.(map[string]any); ok {
				compareSourceDefaults(def["source"])
			}
		}
	}
	if variables, ok := value.(map[string]any); exists && (value == nil || ok && len(variables) == 0) {
		delete(config, "variables")
	}
}

func compareSourceDefaults(value any) {
	if source, ok := value.(map[string]any); ok {
		if suffix, exists := source["suffix"]; exists && suffix == "" {
			delete(source, "suffix")
		}
		if source["bodyMode"] != "form" {
			if form, exists := source["form"]; exists {
				if list, ok := form.([]any); form == nil || ok && len(list) == 0 {
					delete(source, "form")
				}
			}
		}
	}
}
func compareHeaderDefaults(value any) {
	if headers, ok := value.([]any); ok {
		for _, item := range headers {
			if header, ok := item.(map[string]any); ok {
				compareSourceDefaults(header["source"])
			}
		}
	}
}
func compareRequestDefaults(request map[string]any) {
	if http, ok := request["http"].(map[string]any); ok {
		compareHeaderDefaults(http["headers"])
	}
	if grpc, ok := request["grpc"].(map[string]any); ok {
		compareHeaderDefaults(grpc["metadata"])
	}
}
func compareStepsDefaults(value any) {
	if steps, ok := value.([]any); ok {
		for _, item := range steps {
			if step, ok := item.(map[string]any); ok {
				if blocks, ok := step["blocks"].([]any); ok {
					for _, item := range blocks {
						if block, ok := item.(map[string]any); ok {
							compareRequestDefaults(block)
						}
					}
				}
			}
		}
	}
}

// Merge is pure. Both callers retain their data on every conflict or error.
func Merge(target, source Document) (Document, Summary, error) {
	if err := Validate(target); err != nil {
		return Document{}, nil, err
	}
	if err := Validate(source); err != nil {
		return Document{}, nil, err
	}
	next, err := Clone(target)
	if err != nil {
		return Document{}, nil, err
	}
	input, err := Clone(source)
	if err != nil {
		return Document{}, nil, err
	}
	Canonicalize(&next)
	Canonicalize(&input)
	a, b := reflect.ValueOf(&next.Data).Elem(), reflect.ValueOf(input.Data)
	summary := Summary{}
	conflicts := &Conflicts{}
	added := []Conflict{}
	for i := 0; i < a.NumField(); i++ {
		left, right := a.Field(i), b.Field(i)
		group := a.Type().Field(i).Tag.Get("yaml")
		indices := map[string]int{}
		for j := 0; j < left.Len(); j++ {
			indices[key(left.Index(j))] = j
		}
		count := Counts{}
		for j := 0; j < right.Len(); j++ {
			row := right.Index(j)
			id := key(row)
			if old, ok := indices[id]; ok {
				if equivalent(left.Index(old), row) {
					count.Skipped++
				} else {
					conflicts.Items = append(conflicts.Items, Conflict{group, id, "содержимое одного ID различается"})
				}
			} else {
				left.Set(reflect.Append(left, row))
				indices[id] = left.Len() - 1
				count.Added++
				added = append(added, Conflict{group, id, "конфликт итоговой целостности или уникальности"})
			}
		}
		summary[group] = count
	}
	if len(conflicts.Items) > 0 {
		return Document{}, summary, conflicts
	}
	if err = Validate(next); err != nil {
		// Input data and all individual records passed validation. The only new
		// failure can be a collision of their combined graph; list safe IDs.
		return Document{}, summary, &Conflicts{Items: added}
	}
	return next, summary, nil
}
