package main

import (
	"reflect"
	"testing"

	"reproservice/internal/schema"
)

func firedSel(path string) schema.Selector {
	return schema.Selector{LogicalOperation: "checkout", Source: "orders", Target: "payments",
		Method: "POST", NormalizedPath: path, Attempt: 1}
}

// FR03: finish() reports how many times each activated intervention matched, so the
// controller can classify 0 or >1 matches as INAPPLICABLE.
func TestActiveRun_FinishReportsPerInterventionMatchCounts(t *testing.T) {
	a := &activeRun{}
	ivA := schema.Intervention{ID: "a", Selector: firedSel("/charge")}
	ivB := schema.Intervention{ID: "b", Selector: firedSel("/refund")}
	if err := a.activate("run-1", []schema.Intervention{ivA, ivB}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, ok := a.lookup("run-1", firedSel("/charge")); !ok {
			t.Fatal("expected selector a to match")
		}
	}
	if _, ok := a.lookup("run-1", firedSel("/unrelated")); ok {
		t.Fatal("unrelated selector must not match")
	}
	if _, ok := a.lookup("other-run", firedSel("/refund")); ok {
		t.Fatal("a different run id must not match or count")
	}

	fired, err := a.finish("run-1")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"a": 2, "b": 0}
	if !reflect.DeepEqual(fired, want) {
		t.Fatalf("fired=%v, want %v", fired, want)
	}

	if a.runID != "" {
		t.Fatalf("after finish no run may be active, got %q", a.runID)
	}
	if _, ok := a.lookup("run-1", firedSel("/charge")); ok {
		t.Fatal("lookup after finish must not match")
	}
	if _, err := a.finish("run-1"); err == nil {
		t.Fatal("second finish must error")
	}
}

func TestActiveRun_FinishForNonActiveRunErrors(t *testing.T) {
	a := &activeRun{}
	if _, err := a.finish("nobody"); err == nil {
		t.Fatal("finish with nothing active must error")
	}
	if err := a.activate("run-1", []schema.Intervention{{ID: "a", Selector: firedSel("/charge")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.finish("run-2"); err == nil {
		t.Fatal("finish for a non-active run id must error")
	}
	// The failed finish must not have disturbed the active run.
	if a.runID != "run-1" {
		t.Fatalf("active run disturbed: %q", a.runID)
	}
	if err := a.deactivate("run-1"); err != nil {
		t.Fatal(err)
	}
}
