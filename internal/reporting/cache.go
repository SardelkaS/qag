package reporting

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"qag/internal/problem"
	"qag/internal/runs"
	"sync"
	"time"
)

type cacheEntry struct {
	data        []byte
	runID, hash string
	ownerCaseID string
	expected    bool
	at          time.Time
}
type Cache struct {
	mu                   sync.Mutex
	entries              map[string]cacheEntry
	bytes                int
	now                  func() time.Time
	maxEntries, maxBytes int
	ttl                  time.Duration
}

func NewCache() *Cache {
	return &Cache{entries: map[string]cacheEntry{}, now: time.Now, maxEntries: 16, maxBytes: 64 << 20, ttl: 10 * time.Minute}
}
func (c *Cache) remove(id string) { c.bytes -= len(c.entries[id].data); delete(c.entries, id) }
func (c *Cache) expire() {
	for id, e := range c.entries {
		if !e.at.Add(c.ttl).After(c.now()) {
			c.remove(id)
		}
	}
}
func (c *Cache) Put(report *runs.Report) error {
	if report.Document == nil {
		report.Document = BuildDocument(*report)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expire()
	copy := *report
	copy.Images = append([]runs.ReportImage{}, report.Images...)
	for i := range copy.Images {
		copy.Images[i].Data = ""
	}
	copy.Hash = ""
	raw, err := json.Marshal(copy)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	report.Hash = hex.EncodeToString(sum[:])
	copy.Hash = report.Hash
	raw, err = json.Marshal(copy)
	if err != nil {
		return err
	}
	if len(raw) > c.maxBytes {
		return problem.Invalid("report", "Срез отчёта превышает 64 MiB")
	}
	for len(c.entries) >= c.maxEntries || c.bytes+len(raw) > c.maxBytes {
		var oldest string
		var at time.Time
		for id, e := range c.entries {
			if oldest == "" || e.at.Before(at) {
				oldest = id
				at = e.at
			}
		}
		c.remove(oldest)
	}
	c.entries[report.SnapshotID] = cacheEntry{data: raw, runID: report.RunID, hash: report.Hash, ownerCaseID: report.OwnerCaseID, expected: report.IncludeExpected, at: c.now()}
	c.bytes += len(raw)
	return nil
}
func (c *Cache) Get(id, hash, runID string, expected bool) (runs.Report, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expire()
	e, ok := c.entries[id]
	if !ok || e.hash != hash || e.runID != runID || e.expected != expected {
		return runs.Report{}, problem.Conflict("Срез отчёта истёк или не соответствует команде. Подготовьте экспорт заново")
	}
	var report runs.Report
	err := json.Unmarshal(e.data, &report)
	report.OwnerCaseID = e.ownerCaseID
	return report, err
}
