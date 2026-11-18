package oracle

import (
	"context"
	"testing"

	"reproservice/internal/schema"
)

func TestEvaluatePaymentExactlyOnce(t *testing.T) {
	tests := []struct {
		count    int
		violated bool
		kind     schema.ViolationKind
	}{
		{0, true, schema.ViolationMissing},
		{1, false, ""},
		{2, true, schema.ViolationDuplicate},
		{3, true, schema.ViolationDuplicate},
	}
	for _, tc := range tests {
		a := EvaluatePaymentExactlyOnce(tc.count)
		if a.Violated != tc.violated {
			t.Errorf("count=%d: Violated=%v, want %v", tc.count, a.Violated, tc.violated)
		}
		if a.Kind != tc.kind {
			t.Errorf("count=%d: Kind=%q, want %q", tc.count, a.Kind, tc.kind)
		}
		if a.InvariantID != "payment_exactly_once" {
			t.Errorf("count=%d: InvariantID=%q", tc.count, a.InvariantID)
		}
		if a.Expected != "1" {
			t.Errorf("count=%d: Expected=%q, want \"1\"", tc.count, a.Expected)
		}
		wantActual := map[int]string{0: "0", 1: "1", 2: "2", 3: "3"}[tc.count]
		if a.Actual != wantActual {
			t.Errorf("count=%d: Actual=%q, want %q", tc.count, a.Actual, wantActual)
		}
		if a.InfraError {
			t.Errorf("count=%d: a pure verdict must never be an infra error", tc.count)
		}
	}
}

// FR06: an unknown invariant (or a check that cannot run) is an infra problem, never a
// violation. A nil pool proves the pool is not touched on this path (it would panic).
func TestCheck_UnknownInvariantIsInfraErrorNotViolation(t *testing.T) {
	o := New(nil)
	a, err := o.Check(context.Background(), "unknown_invariant", "x")
	if err == nil {
		t.Fatal("expected a non-nil error for an unknown invariant")
	}
	if !a.InfraError {
		t.Fatalf("expected InfraError=true, got %+v", a)
	}
	if a.Violated {
		t.Fatalf("an infra problem must never be reported as a violation: %+v", a)
	}
	if a.InvariantID != "unknown_invariant" {
		t.Fatalf("InvariantID=%q", a.InvariantID)
	}
}
