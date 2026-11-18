package reducer

// Acceptance tests for FR08 (reduction), FR09 (cancellation) and NFR06 (budget).
// Every fake below is deterministic unless a test says otherwise. Nothing is mocked
// between the algorithm and the Executor: the Executor is the boundary under test.

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"
)

type callRec struct {
	ids []string // sorted copy of the candidate
	out Outcome
	err error
}

func (c callRec) key() string { return strings.Join(c.ids, ",") }

// scriptExec delegates each call to fn (call is 1-based) and records every call.
type scriptExec struct {
	fn  func(ctx context.Context, call int, ids []string) (Outcome, error)
	log []callRec
}

func (s *scriptExec) Execute(ctx context.Context, ids []string) (Outcome, error) {
	sorted := append([]string{}, ids...)
	sort.Strings(sorted)
	out, err := s.fn(ctx, len(s.log)+1, sorted)
	s.log = append(s.log, callRec{ids: sorted, out: out, err: err})
	return out, err
}

func hasAll(ids []string, req []string) bool {
	for _, r := range req {
		if !contains(ids, r) {
			return false
		}
	}
	return true
}

// supersetExec reproduces the original failure iff the candidate contains all of req.
func supersetExec(req ...string) *scriptExec {
	return &scriptExec{fn: func(_ context.Context, _ int, ids []string) (Outcome, error) {
		if hasAll(ids, req) {
			return OutcomeOriginalFailure, nil
		}
		return OutcomePass, nil
	}}
}

func setKey(ids []string) string {
	c := append([]string{}, ids...)
	sort.Strings(c)
	return strings.Join(c, ",")
}

var bigBudget = Budget{MaxRuns: 100, MaxDuration: time.Minute}

func ids(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%d", prefix, i)
	}
	return out
}

func run(t *testing.T, ex Executor, all []string, algo Algorithm, b Budget, opts Options) *Result {
	t.Helper()
	res, err := ReduceWithOptions(context.Background(), ex, all, algo, b, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return res
}

// walkDeterministicLog checks that, for a deterministic executor, every screening run
// that reproduced is followed by exactly ConfirmationRuns identical-set runs, and that
// no screened set is executed twice (cache hits are free). It returns the screenings.
func walkDeterministicLog(t *testing.T, log []callRec) (screens []callRec) {
	t.Helper()
	seen := map[string]bool{}
	for i := 0; i < len(log); {
		s := log[i]
		if seen[s.key()] {
			t.Fatalf("set {%s} screened twice (call %d): result cache not used", s.key(), i+1)
		}
		seen[s.key()] = true
		screens = append(screens, s)
		if s.out != OutcomeOriginalFailure {
			i++
			continue
		}
		if i+ConfirmationRuns >= len(log) {
			t.Fatalf("reproducing screening of {%s} at call %d lacks %d confirmations", s.key(), i+1, ConfirmationRuns)
		}
		for j := 1; j <= ConfirmationRuns; j++ {
			c := log[i+j]
			if c.key() != s.key() || c.out != OutcomeOriginalFailure {
				t.Fatalf("call %d should confirm {%s}, got {%s} -> %s", i+j+1, s.key(), c.key(), c.out)
			}
		}
		i += 1 + ConfirmationRuns
	}
	return screens
}

func countReproducing(screens []callRec) int {
	n := 0
	for _, s := range screens {
		if s.out == OutcomeOriginalFailure {
			n++
		}
	}
	return n
}

var allAlgos = []Algorithm{AlgoDDMin, AlgoSequential, AlgoDependencyAware}

// ---------------------------------------------------------------------------
// FR08: correctness / 1-minimality / budget accounting
// ---------------------------------------------------------------------------

func TestProperty_ResultEqualsRequiredSet_AllAlgorithms(t *testing.T) {
	rng := rand.New(rand.NewSource(52))
	for iter := 0; iter < 250; iter++ {
		n := 1 + rng.Intn(8)
		all := ids("e", n)
		var req []string
		for _, id := range all {
			if rng.Float64() < 0.4 {
				req = append(req, id)
			}
		}
		// Random dependency edges influence only ordering (ADR-005), never acceptance.
		deps := map[string][]string{}
		for i := 1; i < n; i++ {
			if rng.Float64() < 0.5 {
				deps[all[i]] = []string{all[rng.Intn(i)]}
			}
		}
		for _, algo := range allAlgos {
			ex := supersetExec(req...)
			opts := Options{}
			if algo == AlgoDependencyAware {
				opts.Order = LeafFirstOrder(deps)
			}
			res := run(t, ex, all, algo, bigBudget, opts)
			name := fmt.Sprintf("iter=%d algo=%s all=%v req=%v", iter, algo, all, req)
			if !sameSet(res.Accepted, req) {
				t.Fatalf("%s: Accepted=%v, want exactly %v", name, res.Accepted, req)
			}
			if res.StopReason != StopMinimal || !res.OneMinimal || !res.Confirmed {
				t.Fatalf("%s: stop=%s oneMinimal=%v confirmed=%v", name, res.StopReason, res.OneMinimal, res.Confirmed)
			}
			if res.RunsUsed > 100 {
				t.Fatalf("%s: RunsUsed=%d exceeds NFR06 limit", name, res.RunsUsed)
			}
			if res.RunsUsed != len(ex.log) {
				t.Fatalf("%s: RunsUsed=%d but executor was called %d times (budget accounting broken)", name, res.RunsUsed, len(ex.log))
			}
			screens := walkDeterministicLog(t, ex.log)
			if want := ConfirmationRuns * countReproducing(screens); res.ConfirmationRuns != want {
				t.Fatalf("%s: ConfirmationRuns=%d, want %d", name, res.ConfirmationRuns, want)
			}
			if res.Algorithm != string(algo) {
				t.Fatalf("%s: Algorithm=%q", name, res.Algorithm)
			}
			if res.Unstable || res.UnstableRejected != 0 {
				t.Fatalf("%s: deterministic fake must never be unstable", name)
			}
			// 1-minimality, checked directly against the oracle of the fake.
			for i := range res.Accepted {
				smaller := append(append([]string{}, res.Accepted[:i]...), res.Accepted[i+1:]...)
				if hasAll(smaller, req) {
					t.Fatalf("%s: Accepted %v not 1-minimal: removing %s still reproduces", name, res.Accepted, res.Accepted[i])
				}
			}
		}
	}
}

func TestOriginalConfirmedBeforeAnySmallerCandidate(t *testing.T) {
	all := []string{"a", "b", "c", "d", "e"}
	ex := supersetExec("c")
	res := run(t, ex, all, AlgoDDMin, bigBudget, Options{})
	if len(ex.log) < 1+ConfirmationRuns+1 {
		t.Fatalf("too few calls: %d", len(ex.log))
	}
	for i := 0; i < 1+ConfirmationRuns; i++ {
		if ex.log[i].key() != setKey(all) {
			t.Fatalf("call %d executed {%s}; the first %d calls must all be the full set", i+1, ex.log[i].key(), 1+ConfirmationRuns)
		}
	}
	if ex.log[1+ConfirmationRuns].key() == setKey(all) {
		t.Fatal("full set executed more than 1+3 times before a smaller candidate")
	}
	if !res.Confirmed {
		t.Fatal("expected Confirmed")
	}
}

func TestUnstableOriginal_ReproducesOnceThenNot(t *testing.T) {
	ex := &scriptExec{fn: func(_ context.Context, call int, _ []string) (Outcome, error) {
		if call == 1 {
			return OutcomeOriginalFailure, nil
		}
		return OutcomePass, nil
	}}
	all := []string{"a", "b", "c"}
	res := run(t, ex, all, AlgoDDMin, bigBudget, Options{})
	if res.StopReason != StopUnstable {
		t.Fatalf("StopReason=%s, want %s", res.StopReason, StopUnstable)
	}
	if !res.Unstable || res.Confirmed || res.OneMinimal {
		t.Fatalf("Unstable=%v Confirmed=%v OneMinimal=%v", res.Unstable, res.Confirmed, res.OneMinimal)
	}
	if !sameSet(res.Accepted, all) {
		t.Fatalf("Accepted=%v, want the input untouched", res.Accepted)
	}
	if len(ex.log) != 2 || res.RunsUsed != 2 {
		t.Fatalf("expected to stop at the first failed confirmation: calls=%d RunsUsed=%d", len(ex.log), res.RunsUsed)
	}
}

func TestEveryAcceptedCandidateIsConfirmedThreeTimes(t *testing.T) {
	ex := supersetExec("c")
	res := run(t, ex, []string{"a", "b", "c", "d", "e", "f", "g", "h"}, AlgoDDMin, bigBudget, Options{})
	screens := walkDeterministicLog(t, ex.log) // asserts screening + 3 identical confirmations
	// The full set plus [a,b,c,d], [c,d], [c] were accepted before the empty probe.
	if got := countReproducing(screens); got < 4 {
		t.Fatalf("expected at least 4 accepted candidates, got %d", got)
	}
	if res.ConfirmationRuns != ConfirmationRuns*countReproducing(screens) {
		t.Fatalf("ConfirmationRuns=%d", res.ConfirmationRuns)
	}
	if res.RunsUsed != len(ex.log) {
		t.Fatalf("RunsUsed=%d calls=%d", res.RunsUsed, len(ex.log))
	}
}

func TestFlakyCandidate_RejectedAndLastConfirmedSetReturned(t *testing.T) {
	// {c,d} is required, but the set exactly {c,d} reproduces only on its first run.
	for _, confirmOutcome := range []Outcome{OutcomePass, OutcomeOtherFailure, OutcomeTimeout, OutcomeInapplicable} {
		for _, algo := range []Algorithm{AlgoDDMin, AlgoSequential} {
			name := fmt.Sprintf("%s/%s", algo, confirmOutcome)
			t.Run(name, func(t *testing.T) {
				seen := 0
				ex := &scriptExec{fn: func(_ context.Context, _ int, ids []string) (Outcome, error) {
					if !hasAll(ids, []string{"c", "d"}) {
						return OutcomePass, nil
					}
					if setKey(ids) == "c,d" {
						seen++
						if seen == 1 {
							return OutcomeOriginalFailure, nil
						}
						return confirmOutcome, nil
					}
					return OutcomeOriginalFailure, nil
				}}
				res := run(t, ex, []string{"a", "b", "c", "d"}, algo, bigBudget, Options{})
				if res.UnstableRejected < 1 || !res.Unstable {
					t.Fatalf("UnstableRejected=%d Unstable=%v", res.UnstableRejected, res.Unstable)
				}
				if sameSet(res.Accepted, []string{"c", "d"}) {
					t.Fatalf("flaky set {c,d} must not be returned, got %v", res.Accepted)
				}
				if !sameSet(res.Accepted, []string{"b", "c", "d"}) {
					t.Fatalf("Accepted=%v, want last confirmed set [b c d]", res.Accepted)
				}
				if !res.Confirmed {
					t.Fatal("last accepted set was confirmed 3/3; Confirmed must be true")
				}
				if res.StopReason != StopMinimal {
					t.Fatalf("search must continue past the flaky candidate to completion, got %s", res.StopReason)
				}
				// {c,d} ran exactly once to screen and once in a failed confirmation.
				if seen != 2 {
					t.Fatalf("{c,d} executed %d times, want 2 (screening + failed confirmation, then cached)", seen)
				}
			})
		}
	}
}

// ---------------------------------------------------------------------------
// NFR06: budgets
// ---------------------------------------------------------------------------

func TestBudgetRuns_NeverExceededAndAcceptedIsLastConfirmed(t *testing.T) {
	all := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	full := all
	half := []string{"a", "b", "c", "d"}
	// Schedule with required={c}: calls 1-4 full set (orig + 3), 5 screens [a b c d],
	// 6-8 confirm it, 9 screens [a b], 10 screens [c d].
	tests := []struct {
		n             int
		wantAccepted  []string
		wantConfirmed bool
		wantConfRuns  int
	}{
		{1, full, false, 0},
		{2, full, false, 1},
		{3, full, false, 2},
		{4, full, true, 3},
		{5, full, true, 3}, // screening of the half done, confirmation not started
		{6, full, true, 4}, // budget hit DURING confirmation: half is NOT accepted
		{7, full, true, 5},
		{8, half, true, 6},
		{9, half, true, 6},
		{10, half, true, 6},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("MaxRuns=%d", tc.n), func(t *testing.T) {
			ex := supersetExec("c")
			res := run(t, ex, all, AlgoDDMin, Budget{MaxRuns: tc.n, MaxDuration: time.Hour}, Options{})
			if len(ex.log) > tc.n {
				t.Fatalf("executor called %d times with MaxRuns=%d", len(ex.log), tc.n)
			}
			if len(ex.log) != tc.n || res.RunsUsed != tc.n {
				t.Fatalf("calls=%d RunsUsed=%d, want exactly %d", len(ex.log), res.RunsUsed, tc.n)
			}
			if res.StopReason != StopBudgetRuns || !res.StopReason.IsBudget() || res.OneMinimal {
				t.Fatalf("StopReason=%s OneMinimal=%v", res.StopReason, res.OneMinimal)
			}
			if !sameSet(res.Accepted, tc.wantAccepted) {
				t.Fatalf("Accepted=%v, want %v", res.Accepted, tc.wantAccepted)
			}
			if res.Confirmed != (tc.n >= 4) {
				t.Fatalf("Confirmed=%v with MaxRuns=%d", res.Confirmed, tc.n)
			}
			if res.ConfirmationRuns != tc.wantConfRuns && tc.n < 4 {
				t.Fatalf("ConfirmationRuns=%d, want %d", res.ConfirmationRuns, tc.wantConfRuns)
			}
			if tc.n >= 4 && res.ConfirmationRuns != tc.wantConfRuns {
				t.Fatalf("ConfirmationRuns=%d, want %d (confirmation runs count toward the budget)", res.ConfirmationRuns, tc.wantConfRuns)
			}
		})
	}
}

func TestBudgetRuns_ApplyToEveryAlgorithm(t *testing.T) {
	for _, algo := range allAlgos {
		for n := 1; n <= 12; n++ {
			ex := supersetExec("c")
			res := run(t, ex, []string{"a", "b", "c", "d", "e", "f"}, algo, Budget{MaxRuns: n, MaxDuration: time.Hour}, Options{})
			if len(ex.log) > n || res.RunsUsed != len(ex.log) {
				t.Fatalf("%s MaxRuns=%d: calls=%d RunsUsed=%d", algo, n, len(ex.log), res.RunsUsed)
			}
			if res.StopReason == StopBudgetRuns && len(ex.log) != n {
				t.Fatalf("%s MaxRuns=%d: stopped on runs budget after only %d calls", algo, n, len(ex.log))
			}
			if !hasAll(res.Accepted, []string{"c"}) {
				t.Fatalf("%s MaxRuns=%d: Accepted=%v lost the required element", algo, n, res.Accepted)
			}
		}
	}
}

func TestBudgetDuration_SlowExecutorStopsOnSeconds(t *testing.T) {
	ex := &scriptExec{fn: func(_ context.Context, _ int, _ []string) (Outcome, error) {
		time.Sleep(30 * time.Millisecond)
		return OutcomeOriginalFailure, nil
	}}
	res := run(t, ex, []string{"a", "b", "c", "d"}, AlgoDDMin, Budget{MaxRuns: 1000, MaxDuration: 100 * time.Millisecond}, Options{})
	if res.StopReason != StopBudgetDuration || !res.StopReason.IsBudget() {
		t.Fatalf("StopReason=%s, want %s", res.StopReason, StopBudgetDuration)
	}
	if res.RunsUsed != len(ex.log) {
		t.Fatalf("RunsUsed=%d calls=%d", res.RunsUsed, len(ex.log))
	}
	if res.RunsUsed >= 10 {
		t.Fatalf("a 100ms budget with 30ms runs allowed %d runs", res.RunsUsed)
	}
	if res.OneMinimal {
		t.Fatal("budget stop must not claim minimality")
	}
}

func TestBudgetDuration_ExecutorBlockedOnContextIsBudgetStopNotInfraError(t *testing.T) {
	ex := &scriptExec{fn: func(ctx context.Context, _ int, _ []string) (Outcome, error) {
		<-ctx.Done()
		return "", fmt.Errorf("run aborted: %w", ctx.Err())
	}}
	start := time.Now()
	res := run(t, ex, []string{"a", "b"}, AlgoDDMin, Budget{MaxRuns: 100, MaxDuration: 60 * time.Millisecond}, Options{})
	if res.StopReason != StopBudgetDuration {
		t.Fatalf("StopReason=%s, want %s (deadline expiry must not be INFRA_ERROR)", res.StopReason, StopBudgetDuration)
	}
	if res.Error != "" {
		t.Fatalf("Error=%q, want empty for a budget stop", res.Error)
	}
	if res.Confirmed || !sameSet(res.Accepted, []string{"a", "b"}) {
		t.Fatalf("Confirmed=%v Accepted=%v", res.Confirmed, res.Accepted)
	}
	if res.RunsUsed != 1 || len(ex.log) != 1 {
		t.Fatalf("RunsUsed=%d calls=%d: the aborted run is charged once", res.RunsUsed, len(ex.log))
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("reduction did not honour the wall-clock budget")
	}
}

func TestBudget_FirstBoundaryWins(t *testing.T) {
	t.Run("runs first", func(t *testing.T) {
		ex := supersetExec("a")
		res := run(t, ex, []string{"a", "b", "c", "d"}, AlgoDDMin, Budget{MaxRuns: 2, MaxDuration: time.Hour}, Options{})
		if res.StopReason != StopBudgetRuns {
			t.Fatalf("StopReason=%s", res.StopReason)
		}
	})
	t.Run("time first", func(t *testing.T) {
		ex := &scriptExec{fn: func(_ context.Context, _ int, _ []string) (Outcome, error) {
			time.Sleep(30 * time.Millisecond)
			return OutcomeOriginalFailure, nil
		}}
		res := run(t, ex, []string{"a", "b", "c", "d"}, AlgoDDMin, Budget{MaxRuns: 100, MaxDuration: 50 * time.Millisecond}, Options{})
		if res.StopReason != StopBudgetDuration {
			t.Fatalf("StopReason=%s, want %s (only %d of 100 runs used)", res.StopReason, StopBudgetDuration, res.RunsUsed)
		}
	})
}

func TestBudgetClamp(t *testing.T) {
	tests := []struct {
		in, want Budget
	}{
		{Budget{1000, time.Hour}, Budget{100, 10 * time.Minute}},
		{Budget{101, 10*time.Minute + time.Second}, Budget{100, 10 * time.Minute}},
		{Budget{5, 30 * time.Second}, Budget{5, 30 * time.Second}},
		{Budget{100, 10 * time.Minute}, Budget{100, 10 * time.Minute}},
		{Budget{1000, time.Second}, Budget{100, time.Second}},
		{Budget{3, time.Hour}, Budget{3, 10 * time.Minute}},
	}
	for _, tc := range tests {
		if got := tc.in.Clamp(); got != tc.want {
			t.Errorf("Clamp(%+v)=%+v, want %+v", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Empty-set probe
// ---------------------------------------------------------------------------

func TestEmptySetProbe_SingleRequiredElement(t *testing.T) {
	for _, algo := range allAlgos {
		t.Run(string(algo), func(t *testing.T) {
			ex := supersetExec("c")
			res := run(t, ex, []string{"a", "b", "c"}, algo, bigBudget, Options{})
			empties := 0
			for _, c := range ex.log {
				if len(c.ids) == 0 {
					empties++
				}
			}
			if empties != 1 {
				t.Fatalf("empty set executed %d times, want exactly 1", empties)
			}
			last := ex.log[len(ex.log)-1]
			if len(last.ids) != 0 || last.out == OutcomeOriginalFailure {
				t.Fatalf("last call must be the rejected empty-set probe, got {%s} -> %s", last.key(), last.out)
			}
			if !sameSet(res.Accepted, []string{"c"}) || !res.OneMinimal || res.StopReason != StopMinimal {
				t.Fatalf("Accepted=%v OneMinimal=%v Stop=%s", res.Accepted, res.OneMinimal, res.StopReason)
			}
		})
	}
}

func TestEmptySetProbe_SingletonInput(t *testing.T) {
	for _, algo := range allAlgos {
		ex := supersetExec("c")
		res := run(t, ex, []string{"c"}, algo, bigBudget, Options{})
		if len(ex.log) != 1+ConfirmationRuns+1 || res.RunsUsed != 5 {
			t.Fatalf("%s: calls=%d RunsUsed=%d, want 4 (original) + 1 (empty probe)", algo, len(ex.log), res.RunsUsed)
		}
		if len(ex.log[4].ids) != 0 {
			t.Fatalf("%s: fifth call must be the empty-set probe", algo)
		}
		if !res.OneMinimal || !sameSet(res.Accepted, []string{"c"}) {
			t.Fatalf("%s: %+v", algo, res)
		}
	}
}

func TestEmptySetReproduces_AcceptedIsEmptyAndConfirmed(t *testing.T) {
	for _, algo := range allAlgos {
		t.Run(string(algo), func(t *testing.T) {
			ex := supersetExec() // no required element: fails with no interventions at all
			res := run(t, ex, []string{"a", "b", "c"}, algo, bigBudget, Options{})
			if len(res.Accepted) != 0 {
				t.Fatalf("Accepted=%v, want empty", res.Accepted)
			}
			if !res.Confirmed || res.StopReason != StopMinimal {
				t.Fatalf("Confirmed=%v Stop=%s", res.Confirmed, res.StopReason)
			}
			n := len(ex.log)
			if n < 4 {
				t.Fatalf("calls=%d", n)
			}
			for i := n - 4; i < n; i++ {
				if len(ex.log[i].ids) != 0 || ex.log[i].out != OutcomeOriginalFailure {
					t.Fatalf("final 4 calls must be the empty set (1 screening + 3 confirmations); call %d = {%s}", i+1, ex.log[i].key())
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// FR09: cancellation, and infrastructure errors
// ---------------------------------------------------------------------------

func TestCancellation_FromInsideExecute(t *testing.T) {
	all := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	half := []string{"a", "b", "c", "d"}
	tests := []struct {
		k          int
		returnErr  bool // false: Execute returns a normal outcome after cancelling
		want       []string
		wantConfd  bool
		wantNoCall bool
	}{
		{1, false, all, false, true},
		{3, false, all, false, true},
		{5, false, all, true, true},  // screening of half done, never confirmed
		{7, false, all, true, true},  // mid-confirmation: half NOT accepted
		{8, false, half, true, true}, // confirmation completed on the very call that cancelled
		{9, false, half, true, true},
		{1, true, all, false, true},
		{6, true, all, true, true},
		{8, true, all, true, true}, // aborted run is discarded: its confirmation is incomplete
		{9, true, half, true, true},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("k=%d/returnErr=%v", tc.k, tc.returnErr), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			inner := supersetExec("c")
			ex := &scriptExec{fn: func(c context.Context, call int, ids []string) (Outcome, error) {
				out, _ := inner.fn(c, call, ids)
				if call == tc.k {
					cancel()
					if tc.returnErr {
						return "", c.Err()
					}
				}
				return out, nil
			}}
			res, err := ReduceWithOptions(ctx, ex, all, AlgoDDMin, bigBudget, Options{})
			if err != nil {
				t.Fatalf("cancellation must not surface as an error, got %v", err)
			}
			if res.StopReason != StopCancelled {
				t.Fatalf("StopReason=%s, want CANCELLED", res.StopReason)
			}
			if len(ex.log) != tc.k {
				t.Fatalf("executor called %d times, want exactly %d (no call after cancellation)", len(ex.log), tc.k)
			}
			if !sameSet(res.Accepted, tc.want) {
				t.Fatalf("Accepted=%v, want last confirmed %v", res.Accepted, tc.want)
			}
			if res.Confirmed != tc.wantConfd {
				t.Fatalf("Confirmed=%v, want %v", res.Confirmed, tc.wantConfd)
			}
			if res.OneMinimal || res.Error == "" {
				t.Fatalf("OneMinimal=%v Error=%q", res.OneMinimal, res.Error)
			}
		})
	}
}

func TestCancellation_BeforeStartRunsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ex := supersetExec("a")
	res, err := ReduceWithOptions(ctx, ex, []string{"a", "b"}, AlgoDDMin, bigBudget, Options{})
	if err != nil || res.StopReason != StopCancelled || len(ex.log) != 0 || res.RunsUsed != 0 {
		t.Fatalf("err=%v res=%+v calls=%d", err, res, len(ex.log))
	}
}

func TestInfraError_OnMthCall_KeepsLastConfirmedSet(t *testing.T) {
	all := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	half := []string{"a", "b", "c", "d"}
	tests := []struct {
		m         int
		want      []string
		wantConfd bool
	}{
		{1, all, false},
		{3, all, false},
		{5, all, true},
		{6, all, true}, // during confirmation of half: not accepted
		{8, all, true},
		{9, half, true},
		{11, half, true},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("m=%d", tc.m), func(t *testing.T) {
			inner := supersetExec("c")
			boom := errors.New("docker compose exploded")
			ex := &scriptExec{fn: func(c context.Context, call int, ids []string) (Outcome, error) {
				if call == tc.m {
					return "", boom
				}
				return inner.fn(c, call, ids)
			}}
			res, err := ReduceWithOptions(context.Background(), ex, all, AlgoDDMin, bigBudget, Options{})
			if err != nil {
				t.Fatalf("infra errors are reported in the Result, not returned: %v", err)
			}
			if res.StopReason != StopInfraError {
				t.Fatalf("StopReason=%s, want INFRA_ERROR", res.StopReason)
			}
			if !strings.Contains(res.Error, "docker compose exploded") {
				t.Fatalf("Error=%q", res.Error)
			}
			if len(ex.log) != tc.m || res.RunsUsed != tc.m {
				t.Fatalf("calls=%d RunsUsed=%d, want %d", len(ex.log), res.RunsUsed, tc.m)
			}
			if !sameSet(res.Accepted, tc.want) {
				t.Fatalf("Accepted=%v, want %v (last confirmed set must not be discarded)", res.Accepted, tc.want)
			}
			if res.Confirmed != (tc.m > 4) {
				t.Fatalf("Confirmed=%v", res.Confirmed)
			}
			if res.OneMinimal {
				t.Fatal("infra error must not claim minimality")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Options.Valid (explicit prerequisites, ADR-005)
// ---------------------------------------------------------------------------

func TestOptionsValid_InvalidCandidatesNeverExecuted_SameResultAcrossAlgorithms(t *testing.T) {
	all := []string{"a", "b", "c", "d", "e"}
	deps := map[string][]string{"c": {"a"}} // c is only valid together with a
	valid := func(ids []string) bool {
		for _, id := range ids {
			for _, d := range deps[id] {
				if !contains(ids, d) {
					return false
				}
			}
		}
		return true
	}
	var results []*Result
	for _, algo := range allAlgos {
		ex := supersetExec("c") // would reproduce on {c} alone, if it were ever executed
		opts := Options{Valid: valid}
		if algo == AlgoDependencyAware {
			opts.Order = LeafFirstOrder(deps)
		}
		res := run(t, ex, all, algo, bigBudget, opts)
		for i, c := range ex.log {
			if !valid(c.ids) {
				t.Fatalf("%s: call %d executed invalid candidate {%s}", algo, i+1, c.key())
			}
		}
		if res.SkippedInvalid == 0 {
			t.Fatalf("%s: SkippedInvalid=0, expected invalid candidates to be skipped", algo)
		}
		if !sameSet(res.Accepted, []string{"a", "c"}) {
			t.Fatalf("%s: Accepted=%v, want [a c]", algo, res.Accepted)
		}
		if res.StopReason != StopMinimal || !res.OneMinimal {
			t.Fatalf("%s: Stop=%s", algo, res.StopReason)
		}
		results = append(results, res)
	}
	for i := 1; i < len(results); i++ {
		if !sameSet(results[0].Accepted, results[i].Accepted) {
			t.Fatalf("%s and %s disagree under the same Valid: %v vs %v", allAlgos[0], allAlgos[i], results[0].Accepted, results[i].Accepted)
		}
	}
}

func TestOptionsValid_InvalidFullSetIsStillTheCallersProblem_NoPanic(t *testing.T) {
	// Valid is not consulted for the original run; only for candidates. A Valid that
	// rejects everything therefore leaves the full set as the confirmed result.
	ex := supersetExec("a")
	res := run(t, ex, []string{"a", "b"}, AlgoDDMin, bigBudget, Options{Valid: func([]string) bool { return false }})
	if !sameSet(res.Accepted, []string{"a", "b"}) || res.StopReason != StopMinimal || res.SkippedInvalid == 0 {
		t.Fatalf("%+v", res)
	}
	if len(ex.log) != 1+ConfirmationRuns {
		t.Fatalf("calls=%d, want only the original confirmation", len(ex.log))
	}
}

// ---------------------------------------------------------------------------
// Result cache
// ---------------------------------------------------------------------------

func TestResultCache_ComplementEqualToOtherChunkIsNotReexecuted(t *testing.T) {
	ex := supersetExec("b", "c") // neither half of [a b c d] reproduces on its own
	res := run(t, ex, []string{"a", "b", "c", "d"}, AlgoDDMin, bigBudget, Options{})
	if res.CacheHits == 0 {
		t.Fatal("CacheHits=0; at n=2 the complements equal the chunks and must be answered from cache")
	}
	walkDeterministicLog(t, ex.log) // fails if any set is screened twice
	if res.RunsUsed != len(ex.log) {
		t.Fatalf("cache hits were charged: RunsUsed=%d calls=%d", res.RunsUsed, len(ex.log))
	}
	if !sameSet(res.Accepted, []string{"b", "c"}) {
		t.Fatalf("Accepted=%v", res.Accepted)
	}
}

// ---------------------------------------------------------------------------
// Sequential
// ---------------------------------------------------------------------------

func TestSequential_RepeatedPassMakesResultOneMinimal(t *testing.T) {
	// Non-monotone failure: removing "a" from the full set stops reproducing, but once
	// "b" is gone, removing "a" does reproduce. Only a repeated pass finds [c].
	reproduces := map[string]bool{"a,b,c": true, "a,c": true, "c": true}
	ex := &scriptExec{fn: func(_ context.Context, _ int, ids []string) (Outcome, error) {
		if reproduces[setKey(ids)] {
			return OutcomeOriginalFailure, nil
		}
		return OutcomePass, nil
	}}
	res := run(t, ex, []string{"a", "b", "c"}, AlgoSequential, bigBudget, Options{})
	if !sameSet(res.Accepted, []string{"c"}) {
		t.Fatalf("Accepted=%v, want [c] (needs a second pass)", res.Accepted)
	}
	if !res.OneMinimal || res.StopReason != StopMinimal {
		t.Fatalf("OneMinimal=%v Stop=%s", res.OneMinimal, res.StopReason)
	}
	last := ex.log[len(ex.log)-1]
	if len(last.ids) != 0 {
		t.Fatalf("with one element left the empty set must be probed last, got {%s}", last.key())
	}
	// "c" alone is screened only after "a,c" has been accepted (second pass).
	idxAC, idxC := -1, -1
	for i, c := range ex.log {
		if c.key() == "a,c" && idxAC < 0 {
			idxAC = i
		}
		if c.key() == "c" && idxC < 0 {
			idxC = i
		}
	}
	if idxAC < 0 || idxC < 0 || idxC < idxAC {
		t.Fatalf("expected [a c] before [c]: idx(a,c)=%d idx(c)=%d", idxAC, idxC)
	}
	walkDeterministicLog(t, ex.log)
}

// ---------------------------------------------------------------------------
// API behaviour
// ---------------------------------------------------------------------------

func TestUnknownAlgorithmIsAnError(t *testing.T) {
	ex := supersetExec("a")
	res, err := ReduceWithOptions(context.Background(), ex, []string{"a"}, Algorithm("bogosort"), bigBudget, Options{})
	if err == nil || res != nil {
		t.Fatalf("res=%v err=%v, want nil result and an error", res, err)
	}
	if len(ex.log) != 0 {
		t.Fatal("nothing may be executed for an unknown algorithm")
	}
	if _, err := Reduce(context.Background(), ex, []string{"a"}, Algorithm(""), bigBudget, nil); err == nil {
		t.Fatal("legacy Reduce wrapper must also reject an unknown algorithm")
	}
}

func TestReduceWrapperStillWorks(t *testing.T) {
	ex := supersetExec("b")
	res, err := Reduce(context.Background(), ex, []string{"a", "b", "c"}, AlgoDDMin, bigBudget, nil)
	if err != nil || !sameSet(res.Accepted, []string{"b"}) || res.StopReason != StopMinimal {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestDependencyAware_OrderingOnly_SameAcceptedSetAsDDMin(t *testing.T) {
	all := []string{"a", "b", "c", "d", "e", "f"}
	deps := map[string][]string{"b": {"a"}, "d": {"a"}, "f": {"e"}}
	for _, req := range [][]string{{"c"}, {"a", "d"}, {"e"}, {"b", "f"}, {}} {
		dd := run(t, supersetExec(req...), all, AlgoDDMin, bigBudget, Options{})
		da := run(t, supersetExec(req...), all, AlgoDependencyAware, bigBudget, Options{Order: LeafFirstOrder(deps)})
		if !sameSet(dd.Accepted, da.Accepted) || !sameSet(dd.Accepted, req) {
			t.Fatalf("req=%v: ddmin=%v dependency_aware=%v", req, dd.Accepted, da.Accepted)
		}
	}
}

func TestOrderIsIgnoredByNonDependencyAwareAlgorithms(t *testing.T) {
	called := false
	order := func(in []string) []string { called = true; return in }
	for _, algo := range []Algorithm{AlgoDDMin, AlgoSequential} {
		run(t, supersetExec("a"), []string{"a", "b", "c"}, algo, bigBudget, Options{Order: order})
		if called {
			t.Fatalf("%s must not apply Options.Order (ordering belongs to the dependency-aware variant)", algo)
		}
	}
}

// ---------------------------------------------------------------------------
// Non-ORIGINAL outcomes are never accepted
// ---------------------------------------------------------------------------

var nonOriginal = []Outcome{OutcomePass, OutcomeOtherFailure, OutcomeTimeout, OutcomeInapplicable}

func TestNonOriginalOutcomeOnCandidate_NeverAccepted(t *testing.T) {
	all := []string{"a", "b", "c"}
	for _, other := range nonOriginal {
		for _, algo := range allAlgos {
			t.Run(fmt.Sprintf("%s/%s", algo, other), func(t *testing.T) {
				ex := &scriptExec{fn: func(_ context.Context, _ int, ids []string) (Outcome, error) {
					if len(ids) == len(all) {
						return OutcomeOriginalFailure, nil
					}
					return other, nil
				}}
				res := run(t, ex, all, algo, bigBudget, Options{})
				if !sameSet(res.Accepted, all) || res.StopReason != StopMinimal || !res.Confirmed {
					t.Fatalf("Accepted=%v Stop=%s Confirmed=%v", res.Accepted, res.StopReason, res.Confirmed)
				}
				if res.Unstable || res.UnstableRejected != 0 {
					t.Fatalf("a non-reproducing screening is not 'unstable': %+v", res)
				}
			})
		}
	}
}

func TestNonOriginalOutcomeOnFirstFullSetRun_OriginalNotReproduced(t *testing.T) {
	for _, other := range nonOriginal {
		for _, algo := range allAlgos {
			t.Run(fmt.Sprintf("%s/%s", algo, other), func(t *testing.T) {
				ex := &scriptExec{fn: func(context.Context, int, []string) (Outcome, error) { return other, nil }}
				res := run(t, ex, []string{"a", "b"}, algo, bigBudget, Options{})
				if res.StopReason != StopOriginalNotReproduced {
					t.Fatalf("StopReason=%s", res.StopReason)
				}
				if res.OriginalOutcome != other {
					t.Fatalf("OriginalOutcome=%q, want %q", res.OriginalOutcome, other)
				}
				if res.Confirmed || res.OneMinimal || res.RunsUsed != 1 || len(ex.log) != 1 {
					t.Fatalf("%+v calls=%d", res, len(ex.log))
				}
			})
		}
	}
}
