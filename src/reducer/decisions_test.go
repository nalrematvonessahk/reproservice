package reducer

import (
	"context"
	"reflect"
	"testing"
)

func countCalls(log []callRec, key string) int {
	n := 0
	for _, c := range log {
		if c.key() == key {
			n++
		}
	}
	return n
}

// Decisions are the source of candidates.accepted / candidates.confirmations: the
// original set first, then one entry per candidate that reproduced on screening.
func TestDecisions_AcceptedCandidatesCarryThreeConfirmations(t *testing.T) {
	ex := supersetExec("c")
	res := run(t, ex, []string{"a", "b", "c", "d", "e"}, AlgoDDMin, bigBudget, Options{})
	if len(res.Decisions) < 2 {
		t.Fatalf("expected original + at least one accepted candidate, got %+v", res.Decisions)
	}
	if first := res.Decisions[0]; !reflect.DeepEqual(first.Candidate, []string{"a", "b", "c", "d", "e"}) || !first.Accepted || first.Confirmations != ConfirmationRuns {
		t.Fatalf("the original set must be decided first and confirmed 3/3: %+v", first)
	}
	var lastAccepted *Decision
	for i := range res.Decisions {
		d := res.Decisions[i]
		if !d.Accepted || d.Confirmations != ConfirmationRuns {
			t.Fatalf("every decision here is a reproducing candidate and must be confirmed 3/3: %+v", d)
		}
		// 1 screening + 3 confirmation executions of exactly that set.
		if n := countCalls(ex.log, setKey(d.Candidate)); n != 1+ConfirmationRuns {
			t.Fatalf("candidate %v executed %d times, want %d", d.Candidate, n, 1+ConfirmationRuns)
		}
		lastAccepted = &res.Decisions[i]
	}
	if !reflect.DeepEqual(setKey(lastAccepted.Candidate), setKey(res.Accepted)) {
		t.Fatalf("the last accepted decision %v must be the reported result %v", lastAccepted.Candidate, res.Accepted)
	}
}

// Non-reproducing, invalid and cached candidates are not decisions.
func TestDecisions_OnlyScreenedReproductionsAreRecorded(t *testing.T) {
	ex := supersetExec("c")
	valid := func(ids []string) bool { return !contains(ids, "e") || contains(ids, "d") }
	res := run(t, ex, []string{"a", "b", "c", "d", "e"}, AlgoSequential, bigBudget, Options{Valid: valid})
	for _, d := range res.Decisions {
		if !hasAll(d.Candidate, []string{"c"}) {
			t.Fatalf("a candidate without the required element cannot have reproduced: %+v", d)
		}
	}
	if res.SkippedInvalid == 0 {
		t.Fatalf("expected invalid candidates to be skipped: %+v", res)
	}
}

// A candidate that reproduces once but fluctuates is rejected, and the number of
// confirmation reruns that did reproduce is recorded.
func TestDecisions_FlakyCandidateRecordedAsRejectedWithPartialConfirmations(t *testing.T) {
	flakyConfirmations := 0
	ex := &scriptExec{fn: func(_ context.Context, _ int, ids []string) (Outcome, error) {
		switch setKey(ids) {
		case "a,b":
			return OutcomeOriginalFailure, nil
		case "a":
			flakyConfirmations++
			// screening (1st) reproduces, confirmation #1 reproduces, #2 fluctuates
			if flakyConfirmations <= 2 {
				return OutcomeOriginalFailure, nil
			}
			return OutcomePass, nil
		}
		return OutcomePass, nil
	}}
	res := run(t, ex, []string{"a", "b"}, AlgoDDMin, bigBudget, Options{})
	var flaky *Decision
	for i := range res.Decisions {
		if setKey(res.Decisions[i].Candidate) == "a" {
			flaky = &res.Decisions[i]
		}
	}
	if flaky == nil || flaky.Accepted || flaky.Confirmations != 1 {
		t.Fatalf("flaky [a] must be rejected with 1 successful confirmation, got %+v (all=%+v)", flaky, res.Decisions)
	}
	if !res.Unstable || !reflect.DeepEqual(res.Accepted, []string{"a", "b"}) {
		t.Fatalf("the last confirmed set must be kept and Unstable set: %+v", res)
	}
}

// Budget ends in the middle of a confirmation: the candidate is NOT accepted and the
// partial count is recorded.
func TestDecisions_BudgetInsideConfirmationRecordsPartialCount(t *testing.T) {
	ex := supersetExec("a")
	// 4 runs confirm the full set {a,b}; run 5 screens [a]; run 6 is its first confirmation.
	res := run(t, ex, []string{"a", "b"}, AlgoDDMin, Budget{MaxRuns: 6, MaxDuration: bigBudget.MaxDuration}, Options{})
	if res.StopReason != StopBudgetRuns || !reflect.DeepEqual(res.Accepted, []string{"a", "b"}) {
		t.Fatalf("expected budget stop keeping the full set, got %v %v", res.StopReason, res.Accepted)
	}
	last := res.Decisions[len(res.Decisions)-1]
	if setKey(last.Candidate) != "a" || last.Accepted || last.Confirmations != 1 {
		t.Fatalf("expected {[a], accepted=false, confirmations=1}, got %+v", last)
	}
}
