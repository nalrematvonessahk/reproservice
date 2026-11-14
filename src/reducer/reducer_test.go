package reducer

import (
	"context"
	"testing"
	"time"
)

// fakeExecutor reproduces OutcomeOriginalFailure iff the candidate set is a superset
// of `required`. This models a scenario where only `required` actually matters and
// every other intervention is a red herring -- exactly what the reducer should find.
type fakeExecutor struct {
	required map[string]bool
	calls    int
}

func newFake(required ...string) *fakeExecutor {
	m := map[string]bool{}
	for _, r := range required {
		m[r] = true
	}
	return &fakeExecutor{required: m}
}

func (f *fakeExecutor) Execute(ctx context.Context, ids []string) (Outcome, error) {
	f.calls++
	present := map[string]bool{}
	for _, id := range ids {
		present[id] = true
	}
	for r := range f.required {
		if !present[r] {
			return OutcomePass, nil
		}
	}
	return OutcomeOriginalFailure, nil
}

func contains(set []string, id string) bool {
	for _, s := range set {
		if s == id {
			return true
		}
	}
	return false
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, v := range a {
		if !contains(b, v) {
			return false
		}
	}
	return true
}

func TestDDMin_ShrinksToRequiredSubset(t *testing.T) {
	exec := newFake("c")
	all := []string{"a", "b", "c", "d", "e"}
	res, err := Reduce(context.Background(), exec, all, AlgoDDMin, Budget{MaxRuns: 100, MaxDuration: time.Minute}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sameSet(res.Accepted, []string{"c"}) {
		t.Fatalf("expected minimal set [c], got %v", res.Accepted)
	}
	if res.StopReason != StopMinimal {
		t.Fatalf("expected StopMinimal, got %v", res.StopReason)
	}
}

func TestSequentialRemoval_ShrinksToRequiredSubset(t *testing.T) {
	exec := newFake("b", "d")
	all := []string{"a", "b", "c", "d", "e"}
	res, err := Reduce(context.Background(), exec, all, AlgoSequential, Budget{MaxRuns: 100, MaxDuration: time.Minute}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sameSet(res.Accepted, []string{"b", "d"}) {
		t.Fatalf("expected minimal set [b d], got %v", res.Accepted)
	}
}

func TestDependencyAware_ShrinksToRequiredSubset(t *testing.T) {
	exec := newFake("c")
	all := []string{"a", "b", "c", "d", "e"}
	order := LeafFirstOrder(map[string][]string{"b": {"a"}}) // a has one dependent (b)
	res, err := Reduce(context.Background(), exec, all, AlgoDependencyAware, Budget{MaxRuns: 100, MaxDuration: time.Minute}, order)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sameSet(res.Accepted, []string{"c"}) {
		t.Fatalf("expected minimal set [c], got %v", res.Accepted)
	}
}

func TestReduce_OriginalNotReproduced(t *testing.T) {
	exec := newFake("never-present")
	all := []string{"a", "b"}
	res, err := Reduce(context.Background(), exec, all, AlgoDDMin, Budget{MaxRuns: 100, MaxDuration: time.Minute}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.StopReason != "ORIGINAL_NOT_REPRODUCED" {
		t.Fatalf("expected ORIGINAL_NOT_REPRODUCED, got %v", res.StopReason)
	}
}

func TestReduce_BudgetExhaustion(t *testing.T) {
	exec := newFake("c")
	all := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	res, err := Reduce(context.Background(), exec, all, AlgoDDMin, Budget{MaxRuns: 1, MaxDuration: time.Minute}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.StopReason != StopBudgetRuns {
		t.Fatalf("expected budget exhaustion with MaxRuns=1, got %v (runsUsed=%d)", res.StopReason, res.RunsUsed)
	}
}

func TestReduce_AllInterventionsRequired_StaysFull(t *testing.T) {
	exec := newFake("a", "b", "c")
	all := []string{"a", "b", "c"}
	res, err := Reduce(context.Background(), exec, all, AlgoDDMin, Budget{MaxRuns: 100, MaxDuration: time.Minute}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sameSet(res.Accepted, all) {
		t.Fatalf("expected no reduction possible, got %v", res.Accepted)
	}
}
