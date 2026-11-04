package reporting

import (
	"bytes"
	"encoding/json"
	"github.com/SardelkaS/qag/internal/runs"
	"testing"
	"time"
)

func TestSnapshotCacheIdentityExpiryBoundsAndImmutability(t *testing.T) {
	now := time.Now()
	cache := NewCache()
	cache.now = func() time.Time { return now }
	cache.maxEntries = 2
	report := runs.Report{SnapshotID: "first", RunID: "run", Title: "old", IncludeExpected: false, Images: []runs.ReportImage{{ID: "image", Data: "image-bytes-must-not-be-cached"}}}
	if err := cache.Put(&report); err != nil {
		t.Fatal(err)
	}
	report.Title = "new"
	report.Document[0].Text = "changed after publication"
	old, err := cache.Get("first", report.Hash, "run", false)
	if err != nil || old.Title != "old" || old.Document[0].Text != "old" {
		t.Fatal("snapshot mutated", err)
	}
	for _, entry := range cache.entries {
		if bytes.Contains(entry.data, []byte("image-bytes")) {
			t.Fatal("image bytes cached")
		}
	}
	for _, wrong := range []struct {
		id, hash, run string
		expected      bool
	}{{"missing", report.Hash, "run", false}, {"first", "wrong", "run", false}, {"first", report.Hash, "other", false}, {"first", report.Hash, "run", true}} {
		if _, err = cache.Get(wrong.id, wrong.hash, wrong.run, wrong.expected); err == nil {
			t.Fatal("foreign snapshot accepted")
		}
	}
	for _, id := range []string{"second", "third"} {
		now = now.Add(time.Second)
		next := runs.Report{SnapshotID: id, RunID: "run"}
		if err = cache.Put(&next); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = cache.Get("first", report.Hash, "run", false); err == nil {
		t.Fatal("oldest snapshot not evicted")
	}
	now = now.Add(11 * time.Minute)
	cache.expire()
	if len(cache.entries) != 0 || cache.bytes != 0 {
		t.Fatal("expired cache retained")
	}
	cache.maxBytes = 20
	if err = cache.Put(&report); err == nil {
		t.Fatal("oversized snapshot accepted")
	}
	if _, err = NewCache().Get("first", report.Hash, "run", false); err == nil {
		t.Fatal("snapshot survived restart")
	}
	raw, _ := json.Marshal(old)
	if bytes.Contains(raw, []byte("changed after")) {
		t.Fatal("aliased document")
	}
}
