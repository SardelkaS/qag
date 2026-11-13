package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/resources"
)

type VariableSourceResult struct {
	Name       string `json:"name"`
	FetchMode  string `json:"fetchMode"`
	State      string `json:"state"`
	StatusCode *int   `json:"statusCode,omitempty"`
	DurationMS int64  `json:"durationMs"`
	ErrorCode  string `json:"errorCode,omitempty"`
}

func (r *Runner) invalidateVariableCache(scope string, res resources.Resolved, traces []VariableSourceResult) {
	state := r.scopeHeaders(scope)
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, trace := range traces {
		prefix := "variable:" + res.Resource.VersionID + ":" + trace.Name + ":"
		for key := range state.cache {
			if strings.HasPrefix(key, prefix) {
				delete(state.cache, key)
			}
		}
	}
	// Keep known values in the redaction set until the scope is closed.
}

func (r *Runner) resolveVariables(ctx context.Context, scope string, block catalog.Block, res resources.Resolved) (catalog.Block, resources.Resolved, []VariableSourceResult, error) {
	traces := []VariableSourceResult{}
	names, err := res.RequiredVariables(block)
	if err != nil {
		return block, res, traces, err
	}
	state := r.scopeHeaders(scope)
	state.mu.Lock()
	defer state.mu.Unlock()
	masks := map[string]string{}
	for k, v := range res.Secrets {
		masks[k] = v
	}
	res.Secrets = masks
	values := map[string]string{}
	var failure error
	for _, name := range names {
		def, _ := res.DynamicVariable(name)
		source, err := res.PrepareVariableSource(*def.Source)
		trace := VariableSourceResult{Name: name, FetchMode: def.Source.FetchMode, State: "fetched"}
		raw, _ := json.Marshal(source)
		digest := sha256.Sum256(raw)
		key := "variable:" + res.Resource.VersionID + ":" + name + ":" + hex.EncodeToString(digest[:])
		value, found := state.cache[key]
		if err == nil && source.FetchMode == "per_run" && found {
			trace.State = "reused"
		} else if err == nil {
			started := time.Now()
			value, trace.StatusCode, err = r.fetchHeader(ctx, source, res)
			trace.DurationMS = time.Since(started).Milliseconds()
			if err == nil {
				value = source.Prefix + value
			}
		}
		if err != nil || len(value) > 64<<10 || !utf8.ValidString(value) {
			trace.State = "error"
			trace.ErrorCode = "variable_source_failed"
			traces = append(traces, trace)
			failure = resources.VariableError(name)
			break
		}
		if source.FetchMode == "per_run" {
			state.cache[key] = value
		}
		state.values[value] = true
		values[name] = value
		traces = append(traces, trace)
	}
	for v := range state.values {
		res.Secrets["qag-source-mask-"+strconv.Itoa(len(res.Secrets))] = v
	}
	res = res.WithDynamicValues(values)
	return block, res, traces, failure
}
