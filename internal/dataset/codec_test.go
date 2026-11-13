package dataset

import (
	"bytes"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SardelkaS/qag/internal/catalog"
)

func sample() Document {
	d := Empty()
	now := time.Now().UTC().Truncate(time.Microsecond)
	id := catalog.NewID()
	_, norm, _ := catalog.Name("Папка")
	d.Data.Folders = []Folder{{ID: id, Name: "Папка", NormalizedName: norm, CreatedAt: now, UpdatedAt: now}}
	d.Data.TestCases = []TestCase{{ID: catalog.NewID(), FolderID: id, Name: "Кейс", NormalizedName: "кейс", Revision: 1, Steps: JSON(`[{"id":"` + catalog.NewID() + `","description":"Кириллица","blocks":[],"legacyNumber":9007199254740993123.125}]`), CreatedAt: now, UpdatedAt: now}}
	return d
}

func TestLegacyResourceVariableDefaultsCompareWithoutRewriting(t *testing.T) {
	old := ResourceVersion{Config: JSON(`{"baseUrl":"http://localhost","tls":{"mode":"plaintext"},"headers":[]}`)}
	current := old
	current.Config = JSON(`{"baseUrl":"http://localhost","tls":{"mode":"plaintext"},"headers":[],"variables":{}}`)
	if !equivalent(reflect.ValueOf(old), reflect.ValueOf(current)) {
		t.Fatal("missing/empty resource variables conflict")
	}
	if strings.Contains(string(old.Config), "variables") {
		t.Fatal("historical JSON rewritten")
	}
	current.Config = JSON(`{"baseUrl":"http://localhost","tls":{"mode":"plaintext"},"headers":[],"variables":{"V":{"mode":"literal","value":"x"}}}`)
	if equivalent(reflect.ValueOf(old), reflect.ValueOf(current)) {
		t.Fatal("real variable change ignored")
	}
}

func TestHTTPSourceDefaultsOnlyNormalizeDefinitionFields(t *testing.T) {
	old := ResourceVersion{Config: JSON(`{"headers":[{"source":{"bodyMode":"text","body":"{\"suffix\":\"\"}"}}],"variables":{"AUTH":{"mode":"http","source":{"bodyMode":"none"}}}}`)}
	current := ResourceVersion{Config: JSON(`{"headers":[{"source":{"bodyMode":"text","body":"{\"suffix\":\"\"}","suffix":"","form":[]}}],"variables":{"AUTH":{"mode":"http","source":{"bodyMode":"none","suffix":"","form":null}}}}`)}
	if !equivalent(reflect.ValueOf(old), reflect.ValueOf(current)) {
		t.Fatal("empty source defaults conflict")
	}
	changed := current
	changed.Config = JSON(strings.Replace(string(current.Config), `"suffix":""`, `"suffix":"]"`, 1))
	if equivalent(reflect.ValueOf(old), reflect.ValueOf(changed)) {
		t.Fatal("real suffix ignored")
	}
	changed = current
	changed.Config = JSON(strings.Replace(string(current.Config), `{\"suffix\":\"\"}`, `{}`, 1))
	if equivalent(reflect.ValueOf(old), reflect.ValueOf(changed)) {
		t.Fatal("user JSON body normalized")
	}
	if strings.Contains(string(old.Config), `"form"`) {
		t.Fatal("original JSON changed")
	}
}

func TestVersionsOneAndTwoReadWithoutRewriting(t *testing.T) {
	current, err := Encode(sample())
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{1, 2} {
		legacy := bytes.Replace(current, []byte("version: 3"), []byte("version: "+strconv.Itoa(version)), 1)
		before := append([]byte(nil), legacy...)
		doc, err := Decode(bytes.NewReader(legacy))
		if err != nil || doc.Version != 3 {
			t.Fatal("legacy decode", err)
		}
		if !bytes.Equal(before, legacy) {
			t.Fatal("source bytes changed")
		}
		if _, err = Encode(doc); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPortableRoundTripAndStrictInput(t *testing.T) {
	d := sample()
	b, err := Encode(d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if got.Data.TestCases[0].Steps != d.Data.TestCases[0].Steps {
		t.Fatal("historical JSON changed")
	}
	again, err := Encode(got)
	if err != nil || !bytes.Equal(b, again) {
		t.Fatal("encoding is not stable", err)
	}
	for name, text := range map[string]string{"unknown": string(b) + "surprise: secret\n", "duplicate": "format: qag\nformat: qag\nversion: 1\ndata: {}\n", "alias": "format: qag\nversion: 1\ndata: &a {folders: *a}\n", "multiple": string(b) + "---\nformat: qag\nversion: 1\ndata: {}\n", "future": strings.Replace(string(b), "version: "+strconv.Itoa(Version), "version: 999", 1), "tag": "format: !danger qag\nversion: 1\ndata: {}\n"} {
		t.Run(name, func(t *testing.T) {
			_, err := Decode(strings.NewReader(text))
			if err == nil {
				t.Fatal("invalid input accepted")
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("input value leaked")
			}
		})
	}
}

func TestMergeAtomicConflictsAndIdempotence(t *testing.T) {
	a := sample()
	b, _ := Clone(a)
	next, counts, err := Merge(a, b)
	if err != nil || counts["folders"].Skipped != 1 {
		t.Fatal(counts, err)
	}
	b.Data.TestCases[0].Revision++
	if _, _, err = Merge(a, b); err == nil {
		t.Fatal("content conflict accepted")
	}
	if a.Data.TestCases[0].Revision != 1 || next.Data.TestCases[0].Revision != 1 {
		t.Fatal("merge mutated input")
	}
	b = sample()
	if _, _, err = Merge(a, b); err == nil {
		t.Fatal("unique name conflict accepted")
	}
	b.Data.Folders[0].Name = "Новая"
	b.Data.Folders[0].NormalizedName = "новая"
	next, _, err = Merge(a, b)
	if err != nil || len(next.Data.Folders) != 2 {
		t.Fatal(err)
	}
	bad, _ := Clone(b)
	bad.Data.TestCases[0].FolderID = catalog.NewID()
	if _, _, err = Merge(a, bad); err == nil {
		t.Fatal("dangling last record accepted")
	}
}

func TestDepthAndRecordLimits(t *testing.T) {
	deep := "format: qag\nversion: 1\ndata: " + strings.Repeat("[", MaxDepth+1) + "0" + strings.Repeat("]", MaxDepth+1)
	if _, err := Decode(strings.NewReader(deep)); err == nil {
		t.Fatal("depth")
	}
	d := Empty()
	d.Data.Folders = make([]Folder, MaxRecords+1)
	if err := Validate(d); err == nil {
		t.Fatal("record limit")
	}
}
