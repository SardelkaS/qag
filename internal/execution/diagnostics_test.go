package execution

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SardelkaS/qag/internal/catalog"
)

func TestDiagnosticProjectionPreservesValuesAndSeparatesLiveResult(t *testing.T) {
	raw := `9007199254740993123.125`
	log := CallLog{Request: LogRequest{Parameters: []catalog.TypedValue{{Type: "numeric", Value: raw}, {Type: "null"}, {Type: "text", Value: ""}}}, Response: &Result{Body: `{"n":` + raw + `}`}}
	data, err := json.Marshal(log)
	if err != nil {
		t.Fatal(err)
	}
	var restored CallLog
	if err = json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Request.Parameters[0].Value != raw || restored.Request.Parameters[1].Type != "null" || restored.Request.Parameters[2].Type != "text" {
		t.Fatal("precision or null lost")
	}
	secret := "probe-only-token"
	view := ProbeView{Probe: HTTPVariableProbe{State: "completed"}, ExtractedValue: &secret, FormattedValue: &secret}
	data, _ = json.Marshal(view.Probe)
	if strings.Contains(string(data), secret) || strings.Contains(string(data), "extractedValue") {
		t.Fatal("live fields in durable projection")
	}
}
