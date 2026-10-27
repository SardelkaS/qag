package runs

import (
	"qag/internal/catalog"
	"qag/internal/comparison"
	"qag/internal/execution"
	"testing"
)

func TestOnlyOriginalAttemptsDetermineOutcome(t *testing.T) {
	base := Run{Snapshot: catalog.TestCase{Steps: []catalog.Step{{Blocks: []catalog.Block{{ID: "block"}}}}}, StepStates: []StepState{{Status: "passed"}}}
	attempt := func(variant, status string) execution.Attempt {
		return execution.Attempt{BlockID: "block", Number: 1, VariantID: variant, State: "completed", Result: &execution.Result{Comparison: comparison.Result{Status: status}}}
	}
	for _, test := range []struct {
		name     string
		attempts []execution.Attempt
		expected string
	}{{"failed variant", []execution.Attempt{attempt("", "match"), attempt("research", "mismatch")}, "passed"}, {"variant cannot replace missing original", []execution.Attempt{attempt("research", "match")}, "blocked"}, {"variant cannot repair original", []execution.Attempt{attempt("", "mismatch"), attempt("research", "match")}, "failed"}} {
		t.Run(test.name, func(t *testing.T) {
			run := base
			run.Attempts = test.attempts
			if got := Evaluate(run); got != test.expected {
				t.Fatalf("got %s", got)
			}
		})
	}
	base.Attempts = []execution.Attempt{attempt("", "match")}
	base.Batches = []Batch{{State: "running"}}
	if Evaluate(base) != "blocked" {
		t.Fatal("active batch evaluated as passed")
	}
}
