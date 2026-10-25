package catalog

import (
	"encoding/json"
	"testing"
)

func TestDraftIsDurableButNotExecutable(t *testing.T) {
	steps := []Step{{ID: NewID(), Blocks: []Block{{ID: NewID(), Kind: "grpc", GRPC: &GRPCRequest{Body: `{"partial":`}}}}}
	if err := ValidateDraft(steps); err != nil {
		t.Fatal(err)
	}
	if err := ValidateExecutable(steps); err == nil {
		t.Fatal("incomplete draft executable")
	}
	encoded, _ := json.Marshal(steps)
	var saved []Step
	if err := json.Unmarshal(encoded, &saved); err != nil {
		t.Fatal(err)
	}
	if saved[0].Blocks[0].GRPC.Body != `{"partial":` {
		t.Fatal("draft input changed")
	}
	steps[0].Blocks[0].ID = steps[0].ID
	if ValidateDraft(steps) == nil {
		t.Fatal("duplicate ID accepted")
	}
}
func TestReadinessDoesNotMutateAndManualImageIsMeaningful(t *testing.T) {
	code := 200
	b := Block{ID: NewID(), Kind: "http", Resource: "GW", HTTP: &HTTPRequest{Method: "GET", BodyMode: "none"}, Expected: Expectation{StatusCode: &code, BodyMode: "none"}}
	steps := []Step{{ID: NewID(), Blocks: []Block{b}}}
	if err := ValidateExecutable(steps); err != nil {
		t.Fatal(err)
	}
	if steps[0].Blocks[0].TimeoutSeconds != 0 {
		t.Fatal("readiness mutated draft")
	}
	steps = []Step{{ID: NewID(), Expected: &StepExpectation{Images: []ImageRef{{ImageID: NewID()}}}}}
	if err := ValidateExecutable(steps); err != nil {
		t.Fatal(err)
	}
	for len(steps[0].Expected.Images) < 11 {
		steps[0].Expected.Images = append(steps[0].Expected.Images, ImageRef{ImageID: NewID()})
	}
	if ValidateDraft(steps) == nil {
		t.Fatal("image limit bypassed")
	}
}
func TestLegacyStaticHeadersDecode(t *testing.T) {
	var b Block
	if err := json.Unmarshal([]byte(`{"id":"legacy","kind":"http","http":{"headers":[{"name":"Authorization","value":"Bearer ${TOKEN}"}]}}`), &b); err != nil {
		t.Fatal(err)
	}
	if b.HTTP.Headers[0].Source != nil || b.HTTP.Headers[0].Value != "Bearer ${TOKEN}" || len(b.Expected.Images) != 0 {
		t.Fatal("legacy model changed")
	}
}
