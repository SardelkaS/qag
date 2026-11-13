package service

import (
	"fmt"
	"testing"
	"time"
)

func TestProbeValueCacheTTLAndEviction(t *testing.T) {
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	e := &Engine{probeValues: map[string]probeValue{}, probeNow: func() time.Time { return at }}
	for i := 0; i < 129; i++ {
		e.putProbeValue(fmt.Sprint(i), probeValue{raw: "sentinel", formatted: "Bearer sentinel", kind: "string"})
		at = at.Add(time.Second)
	}
	if len(e.probeValues) != 128 {
		t.Fatal("unbounded cache", len(e.probeValues))
	}
	if _, ok := e.probeValues["0"]; ok {
		t.Fatal("oldest value not evicted")
	}
	at = at.Add(11 * time.Minute)
	e.putProbeValue("latest", probeValue{raw: ""})
	if len(e.probeValues) != 1 {
		t.Fatal("expired values retained")
	}
}
