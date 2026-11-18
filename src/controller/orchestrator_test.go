package main

// Unit tests for the orchestrator's lifecycle, outcome classification, cancellation and
// reduction wiring (FR02, FR06, FR08, FR09, NFR06). The orchestrator's collaborators are
// replaced by one in-memory `world` that records every call in a single ordered log, so
// the tests can assert call ORDER (e.g. "reset before proxy activation") and not just
// that something was called. No Docker, network or database is used.

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"reproservice/internal/schema"
	"reproservice/src/oracle"
	"reproservice/src/reducer"
)

type decisionRec struct {
	reductionID   string
	ids           []string
	accepted      bool
	confirmations int
}

type world struct {
	mu sync.Mutex

	log             []string
	runs            map[string]*schema.Run
	transitions     map[string][]schema.RunState
	updateStateArgs map[string][]schema.RunState // every UpdateState call, in order, per run
	outcomes        map[string]schema.Outcome
	infra           map[string]string
	assertions      map[string]schema.Assertion
	reductions      []schema.Reduction
	decisions       []decisionRec
	candidateRuns   []string
	failState       map[schema.RunState]error

	active          map[string][]string
	activateCount   map[string]int
	deactivateCount map[string]int
	resetCalls      int
	checkoutCalls   int
	oracleCalls     int

	resetErr func(n int) error
	onReset  func(n int)                                                    // runs outside the lock
	fired    func(runID string, active []string) map[string]int             // nil: every active id fired once
	checkout func(ctx context.Context, runID string, active []string) error // nil: success
	oracleFn func(runID string, active []string) (schema.Assertion, error)  // nil: invariant holds
}

func newWorld() *world {
	return &world{
		runs: map[string]*schema.Run{}, transitions: map[string][]schema.RunState{},
		updateStateArgs: map[string][]schema.RunState{},
		outcomes:        map[string]schema.Outcome{}, infra: map[string]string{},
		assertions: map[string]schema.Assertion{}, failState: map[schema.RunState]error{},
		active: map[string][]string{}, activateCount: map[string]int{}, deactivateCount: map[string]int{},
	}
}

func (w *world) note(format string, a ...any) { w.log = append(w.log, fmt.Sprintf(format, a...)) }

func (w *world) addRun(id string, state schema.RunState, activeIDs []string) *schema.Run {
	w.mu.Lock()
	defer w.mu.Unlock()
	r := &schema.Run{RunID: id, ScenarioID: "sc1", VersionID: "v1", IdempotencyKey: id, State: state, ActiveIDs: activeIDs}
	cp := *r
	w.runs[id] = &cp
	return r
}

func (w *world) state(id string) schema.RunState {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.runs[id].State
}

func (w *world) logCopy() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string{}, w.log...)
}

func (w *world) count(prefix string) int {
	n := 0
	for _, l := range w.logCopy() {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

// ---- runStore ----

func (w *world) CreateRun(_ context.Context, run *schema.Run) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, dup := w.runs[run.RunID]; dup {
		return false, fmt.Errorf("duplicate key value violates unique constraint runs_pkey (%s)", run.RunID)
	}
	cp := *run
	w.runs[run.RunID] = &cp
	w.candidateRuns = append(w.candidateRuns, run.RunID)
	w.note("create:%s", run.RunID)
	return false, nil
}

// UpdateState enforces the real state machine, exactly like store.UpdateState.
func (w *world) UpdateState(_ context.Context, runID string, to schema.RunState) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.updateStateArgs[runID] = append(w.updateStateArgs[runID], to)
	r, ok := w.runs[runID]
	if !ok {
		return fmt.Errorf("run %s not found", runID)
	}
	if err := w.failState[to]; err != nil {
		return err
	}
	if !schema.CanTransition(r.State, to) {
		return fmt.Errorf("store: illegal transition %s -> %s for run %s", r.State, to, runID)
	}
	r.State = to
	w.transitions[runID] = append(w.transitions[runID], to)
	w.note("state:%s", to)
	return nil
}

func (w *world) SetOutcome(_ context.Context, runID string, o schema.Outcome, _ string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.outcomes[runID] = o
	w.runs[runID].State = schema.StateCompleted
	w.note("outcome:%s", o)
	return nil
}

func (w *world) SetInfraError(_ context.Context, runID, reason string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.infra[runID] = reason
	w.runs[runID].State = schema.StateInfraError
	w.note("infra_error")
	return nil
}

func (w *world) InsertAssertion(_ context.Context, runID string, a schema.Assertion) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.assertions[runID] = a
	return nil
}

func (w *world) InsertCandidate(_ context.Context, _, _, _ string, _ []string, _ schema.Outcome, _ bool, _ int) error {
	return nil
}

func (w *world) SaveReduction(_ context.Context, r schema.Reduction) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reductions = append(w.reductions, r)
	return nil
}

// ---- resetter ----

func (w *world) SetCandidateDecision(_ context.Context, reductionID string, ids []string, accepted bool, confirmations int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.decisions = append(w.decisions, decisionRec{reductionID, append([]string{}, ids...), accepted, confirmations})
	return nil
}

func (w *world) Reset(_ context.Context) error {
	w.mu.Lock()
	w.resetCalls++
	n := w.resetCalls
	w.note("reset")
	hook := w.onReset
	errFn := w.resetErr
	w.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	if errFn != nil {
		return errFn(n)
	}
	return nil
}

// ---- proxyAPI ----

func (w *world) Activate(runID string, ivs []schema.Intervention) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	var ids []string
	for _, iv := range ivs {
		ids = append(ids, iv.ID)
	}
	sort.Strings(ids)
	w.active[runID] = ids
	w.activateCount[runID]++
	w.note("activate")
	return nil
}

func (w *world) Deactivate(runID string) (map[string]int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.deactivateCount[runID]++
	w.note("deactivate")
	act := w.active[runID]
	if w.fired != nil {
		return w.fired(runID, act), nil
	}
	m := map[string]int{}
	for _, id := range act {
		m[id] = 1
	}
	return m, nil
}

// ---- sutAPI ----

func (w *world) Checkout(ctx context.Context, _, _, runID string) (*CheckoutResult, error) {
	w.mu.Lock()
	w.checkoutCalls++
	w.note("checkout")
	fn := w.checkout
	act := append([]string{}, w.active[runID]...)
	w.mu.Unlock()
	if fn != nil {
		if err := fn(ctx, runID, act); err != nil {
			return nil, err
		}
	}
	return &CheckoutResult{OrderID: runID}, nil
}

// ---- invariantChecker ----

func (w *world) Check(_ context.Context, _, orderID string) (schema.Assertion, error) {
	w.mu.Lock()
	w.oracleCalls++
	w.note("check")
	fn := w.oracleFn
	act := append([]string{}, w.active[orderID]...)
	w.mu.Unlock()
	if fn != nil {
		return fn(orderID, act)
	}
	return oracle.EvaluatePaymentExactlyOnce(1), nil
}

func (w *world) orch() *Orchestrator { return NewOrchestrator(w, w, w, w, w) }

// ---- scenario / stack helpers ----

func hasID(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func iv(id string, deps ...string) schema.Intervention {
	return schema.Intervention{
		ID: id, DependsOn: deps,
		Selector: schema.Selector{LogicalOperation: "checkout-001", Source: "orders", Target: "payments",
			Method: "POST", NormalizedPath: "/pay/" + id, Attempt: 1},
		Action: schema.Action{Type: schema.ActionDropResponseAfterSuccess},
	}
}

func scenario(runs, seconds int, ivs ...schema.Intervention) *schema.Scenario {
	return &schema.Scenario{
		SchemaVersion: schema.SchemaVersion, ID: "sc1", InvariantID: "payment_exactly_once",
		Budget: schema.Budget{Runs: runs, Seconds: seconds}, Interventions: ivs,
	}
}

// requireFault makes `required` the only intervention that matters: whenever it is
// active the (fake) SUT loses the response and the oracle sees a duplicate payment.
func (w *world) requireFault(required string) {
	w.checkout = func(_ context.Context, _ string, active []string) error {
		if hasID(active, required) {
			return errors.New("sutclient: checkout returned 502")
		}
		return nil
	}
	w.oracleFn = func(_ string, active []string) (schema.Assertion, error) {
		if hasID(active, required) {
			return oracle.EvaluatePaymentExactlyOnce(2), nil
		}
		return oracle.EvaluatePaymentExactlyOnce(1), nil
	}
}

type fakeNetTimeout struct{}

func (fakeNetTimeout) Error() string   { return "i/o timeout" }
func (fakeNetTimeout) Timeout() bool   { return true }
func (fakeNetTimeout) Temporary() bool { return true }

// ---------------------------------------------------------------------------
// classify
// ---------------------------------------------------------------------------

func TestClassify(t *testing.T) {
	dup := oracle.EvaluatePaymentExactlyOnce(2)
	triple := oracle.EvaluatePaymentExactlyOnce(3)
	missing := oracle.EvaluatePaymentExactlyOnce(0)
	ok := oracle.EvaluatePaymentExactlyOnce(1)
	boom := errors.New("sutclient: checkout returned 502")
	both := []string{"a", "b"}
	onceEach := map[string]int{"a": 1, "b": 1}

	tests := []struct {
		name       string
		checkout   error
		timedOut   bool
		a          schema.Assertion
		active     []string
		fired      map[string]int
		want       schema.Outcome
		wantDetail string
	}{
		{"timeout beats duplicate assertion", context.DeadlineExceeded, true, dup, both, onceEach, schema.OutcomeTimeout, "timeout"},
		{"timeout beats inapplicable", nil, true, ok, both, map[string]int{"a": 0, "b": 0}, schema.OutcomeTimeout, ""},
		{"timeout with clean invariant is still TIMEOUT", nil, true, ok, both, onceEach, schema.OutcomeTimeout, ""},
		{"intervention fired zero times", nil, false, ok, both, map[string]int{"a": 1, "b": 0}, schema.OutcomeInapplicable, "b matched 0"},
		{"intervention fired twice (ambiguous selector)", nil, false, ok, both, map[string]int{"a": 2, "b": 1}, schema.OutcomeInapplicable, "a matched 2"},
		{"missing key in fired map counts as zero", nil, false, ok, both, map[string]int{"a": 1}, schema.OutcomeInapplicable, "b matched 0"},
		{"nil fired map with active interventions", nil, false, ok, both, nil, schema.OutcomeInapplicable, ""},
		{"inapplicable beats original failure", boom, false, dup, both, map[string]int{"a": 0, "b": 1}, schema.OutcomeInapplicable, ""},
		{"all fired exactly once and clean is not inapplicable", nil, false, ok, both, onceEach, schema.OutcomePass, ""},
		{"fired entries for non-active ids are ignored", nil, false, ok, []string{"a"}, map[string]int{"a": 1, "zzz": 7}, schema.OutcomePass, ""},
		{"no interventions, clean", nil, false, ok, nil, nil, schema.OutcomePass, ""},
		{"duplicate violation is the original failure", nil, false, dup, both, onceEach, schema.OutcomeOriginalFailure, "expected 1, actual 2"},
		{"triple payment is still duplicate", nil, false, triple, both, onceEach, schema.OutcomeOriginalFailure, "actual 3"},
		{"duplicate violation with lost response (checkout error)", boom, false, dup, both, onceEach, schema.OutcomeOriginalFailure, ""},
		{"missing payment is a different failure", nil, false, missing, both, onceEach, schema.OutcomeOtherFailure, "missing"},
		{"missing payment with checkout error", boom, false, missing, both, onceEach, schema.OutcomeOtherFailure, "missing"},
		{"checkout error with exactly one payment", boom, false, ok, both, onceEach, schema.OutcomeOtherFailure, "checkout failed"},
		{"clean", nil, false, ok, both, onceEach, schema.OutcomePass, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, detail := classify(tc.checkout, tc.timedOut, tc.a, tc.active, tc.fired)
			if got != tc.want {
				t.Fatalf("outcome=%s (%s), want %s", got, detail, tc.want)
			}
			if tc.wantDetail != "" && !strings.Contains(detail, tc.wantDetail) {
				t.Fatalf("detail=%q, want it to contain %q", detail, tc.wantDetail)
			}
			if got == schema.OutcomePass && detail != "" {
				t.Fatalf("PASS carries no detail, got %q", detail)
			}
		})
	}
}

func TestClassify_TimeoutNeverOriginalFailure_Exhaustive(t *testing.T) {
	for _, a := range []schema.Assertion{
		oracle.EvaluatePaymentExactlyOnce(0), oracle.EvaluatePaymentExactlyOnce(1),
		oracle.EvaluatePaymentExactlyOnce(2), oracle.EvaluatePaymentExactlyOnce(5),
	} {
		for _, fired := range []map[string]int{nil, {"a": 1}, {"a": 2}} {
			for _, err := range []error{nil, errors.New("x")} {
				got, _ := classify(err, true, a, []string{"a"}, fired)
				if got != schema.OutcomeTimeout {
					t.Fatalf("timedOut=true must give TIMEOUT, got %s (a=%+v fired=%v err=%v)", got, a, fired, err)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// validCandidate
// ---------------------------------------------------------------------------

func TestValidCandidate(t *testing.T) {
	sc := scenario(10, 60,
		iv("a"), iv("b"),
		iv("c", "a"),      // c needs a
		iv("d", "a", "b"), // d needs a and b
		iv("e", "c"),      // chain e -> c -> a
	)
	valid := validCandidate(sc)
	tests := []struct {
		ids  []string
		want bool
	}{
		{nil, true},
		{[]string{}, true},
		{[]string{"a"}, true},
		{[]string{"b"}, true},
		{[]string{"c"}, false},
		{[]string{"a", "c"}, true},
		{[]string{"c", "a"}, true},
		{[]string{"d", "a"}, false},
		{[]string{"d", "a", "b"}, true},
		{[]string{"e"}, false},
		{[]string{"e", "c"}, false}, // c's own prerequisite a is missing
		{[]string{"e", "c", "a"}, true},
		{[]string{"a", "b", "c", "d", "e"}, true},
		{[]string{"a", "b", "d", "e"}, false},
		{[]string{"unknown"}, true}, // no declared prerequisites
	}
	for _, tc := range tests {
		if got := valid(tc.ids); got != tc.want {
			t.Errorf("validCandidate(%v)=%v, want %v", tc.ids, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// executeOnce
// ---------------------------------------------------------------------------

func TestExecuteOnce_HappyPath_StateMachineAndCallOrder(t *testing.T) {
	w := newWorld()
	w.addRun("r1", schema.StateValidating, []string{"f1"})
	sc := scenario(10, 60, iv("f1"), iv("f2"))
	out, _, err := w.orch().executeOnce(context.Background(), "r1", sc, []string{"f1"})
	if err != nil || out != schema.OutcomePass {
		t.Fatalf("out=%s err=%v", out, err)
	}
	want := []string{"state:RESETTING", "reset", "state:READY", "activate", "state:RUNNING",
		"checkout", "deactivate", "state:ASSERTING", "check"}
	if got := w.logCopy(); !reflect.DeepEqual(got, want) {
		t.Fatalf("call order:\n got %v\nwant %v", got, want)
	}
	wantStates := []schema.RunState{schema.StateResetting, schema.StateReady, schema.StateRunning, schema.StateAsserting}
	if got := w.transitions["r1"]; !reflect.DeepEqual(got, wantStates) {
		t.Fatalf("states=%v, want %v", got, wantStates)
	}
	if w.deactivateCount["r1"] != 1 {
		t.Fatalf("Deactivate called %d times, want exactly once", w.deactivateCount["r1"])
	}
	if got := w.active["r1"]; !reflect.DeepEqual(got, []string{"f1"}) {
		t.Fatalf("only the requested intervention may be activated, got %v", got)
	}
	if _, ok := w.assertions["r1"]; !ok {
		t.Fatal("assertion must be persisted")
	}
}

func TestExecuteOnce_ResetFailure_NothingElseRuns_FR02(t *testing.T) {
	resetErr := errors.New("docker compose down failed")
	w := newWorld()
	w.resetErr = func(int) error { return resetErr }
	w.addRun("r1", schema.StateValidating, []string{"f1"})
	out, _, err := w.orch().executeOnce(context.Background(), "r1", scenario(10, 60, iv("f1")), []string{"f1"})
	if err == nil || !errors.Is(err, resetErr) {
		t.Fatalf("err=%v, want the reset error", err)
	}
	if out != "" {
		t.Fatalf("outcome=%q: a failed reset is never an outcome", out)
	}
	if len(w.activateCount) != 0 || w.checkoutCalls != 0 || w.oracleCalls != 0 || len(w.deactivateCount) != 0 {
		t.Fatalf("after failed reset: activate=%v checkout=%d oracle=%d deactivate=%v; all must be untouched",
			w.activateCount, w.checkoutCalls, w.oracleCalls, w.deactivateCount)
	}
	if got := w.transitions["r1"]; !reflect.DeepEqual(got, []schema.RunState{schema.StateResetting}) {
		t.Fatalf("states=%v", got)
	}
}

func TestExecuteOnce_OracleInfraErrorAndOracleErrorAreErrorsNotOutcomes_FR06(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   func(string, []string) (schema.Assertion, error)
	}{
		{"infra assertion without error", func(string, []string) (schema.Assertion, error) {
			return schema.Assertion{InvariantID: "payment_exactly_once", InfraError: true}, nil
		}},
		{"infra assertion with error", func(string, []string) (schema.Assertion, error) {
			return schema.Assertion{InvariantID: "payment_exactly_once", InfraError: true}, errors.New("db down")
		}},
		{"plain error", func(string, []string) (schema.Assertion, error) {
			return schema.Assertion{}, errors.New("db down")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld()
			w.oracleFn = tc.fn
			w.addRun("r1", schema.StateValidating, []string{"f1"})
			out, _, err := w.orch().executeOnce(context.Background(), "r1", scenario(10, 60, iv("f1")), []string{"f1"})
			if err == nil || out != "" {
				t.Fatalf("out=%q err=%v: an oracle infra problem must be an error, never an outcome/violation", out, err)
			}
			if w.deactivateCount["r1"] != 1 {
				t.Fatalf("Deactivate called %d times, want 1", w.deactivateCount["r1"])
			}
		})
	}
}

func TestExecuteOnce_DeactivateExactlyOnceEvenOnEarlyError(t *testing.T) {
	sc := scenario(10, 60, iv("f1"))
	for _, failAt := range []schema.RunState{schema.StateRunning, schema.StateAsserting} {
		t.Run(string(failAt), func(t *testing.T) {
			w := newWorld()
			w.failState[failAt] = errors.New("db unavailable")
			w.addRun("r1", schema.StateValidating, []string{"f1"})
			_, _, err := w.orch().executeOnce(context.Background(), "r1", sc, []string{"f1"})
			if err == nil {
				t.Fatal("expected error")
			}
			if w.activateCount["r1"] != 1 || w.deactivateCount["r1"] != 1 {
				t.Fatalf("activate=%d deactivate=%d, want 1/1 (proxy scope must always be released)", w.activateCount["r1"], w.deactivateCount["r1"])
			}
			if failAt == schema.StateRunning && w.checkoutCalls != 0 {
				t.Fatal("checkout must not run when -> RUNNING failed")
			}
			if failAt == schema.StateAsserting && w.oracleCalls != 0 {
				t.Fatal("oracle must not run when -> ASSERTING failed")
			}
		})
	}
}

func TestExecuteOnce_CheckoutTimeouts_AreTimeoutNeverOriginalFailure(t *testing.T) {
	sc := scenario(10, 60, iv("f1"))
	dupOracle := func(string, []string) (schema.Assertion, error) { return oracle.EvaluatePaymentExactlyOnce(2), nil }
	tests := []struct {
		name     string
		timeout  time.Duration
		checkout func(ctx context.Context, runID string, active []string) error
	}{
		{"context.DeadlineExceeded", 0, func(context.Context, string, []string) error { return context.DeadlineExceeded }},
		{"wrapped DeadlineExceeded", 0, func(context.Context, string, []string) error {
			return fmt.Errorf("sutclient: checkout request: %w", context.DeadlineExceeded)
		}},
		{"net.Error with Timeout()==true", 0, func(context.Context, string, []string) error {
			return fmt.Errorf("sutclient: checkout request: %w", fakeNetTimeout{})
		}},
		{"tiny checkoutTimeout, SUT blocks on ctx", 20 * time.Millisecond, func(ctx context.Context, _ string, _ []string) error {
			<-ctx.Done()
			return ctx.Err()
		}},
		{"tiny checkoutTimeout, SUT blocks and returns an unrelated error", 20 * time.Millisecond, func(ctx context.Context, _ string, _ []string) error {
			<-ctx.Done()
			return errors.New("connection reset")
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld()
			w.checkout = tc.checkout
			w.oracleFn = dupOracle
			w.addRun("r1", schema.StateValidating, []string{"f1"})
			o := w.orch()
			if tc.timeout > 0 {
				o.checkoutTimeout = tc.timeout
			}
			out, _, err := o.executeOnce(context.Background(), "r1", sc, []string{"f1"})
			if err != nil {
				t.Fatalf("a timeout is an outcome, not an error: %v", err)
			}
			if out != schema.OutcomeTimeout {
				t.Fatalf("outcome=%s, want TIMEOUT even though the oracle saw a duplicate payment", out)
			}
		})
	}
}

func TestExecuteOnce_NonTimeoutCheckoutError(t *testing.T) {
	sc := scenario(10, 60, iv("f1"))
	w := newWorld()
	w.checkout = func(context.Context, string, []string) error { return errors.New("sutclient: checkout returned 500") }
	w.addRun("r1", schema.StateValidating, []string{"f1"})
	out, detail, err := w.orch().executeOnce(context.Background(), "r1", sc, []string{"f1"})
	if err != nil || out != schema.OutcomeOtherFailure || !strings.Contains(detail, "500") {
		t.Fatalf("out=%s detail=%q err=%v, want OTHER_FAILURE", out, detail, err)
	}
}

func TestExecuteOnce_InapplicableWhenProxyReportsWrongMatchCount(t *testing.T) {
	sc := scenario(10, 60, iv("f1"))
	for _, n := range []int{0, 2} {
		w := newWorld()
		w.fired = func(string, []string) map[string]int { return map[string]int{"f1": n} }
		w.requireFault("f1") // even with a duplicate payment, a bad match count wins
		w.addRun("r1", schema.StateValidating, []string{"f1"})
		out, _, err := w.orch().executeOnce(context.Background(), "r1", sc, []string{"f1"})
		if err != nil || out != schema.OutcomeInapplicable {
			t.Fatalf("fired=%d: out=%s err=%v, want INAPPLICABLE", n, out, err)
		}
	}
}

// ---------------------------------------------------------------------------
// ExecuteRun: terminal states, cancellation (FR09), infra errors
// ---------------------------------------------------------------------------

func TestExecuteRun_CompletedWithOutcome_ReleasesStand(t *testing.T) {
	w := newWorld()
	w.requireFault("f1")
	run := w.addRun("r1", schema.StateValidating, []string{"f1", "f2"})
	o := w.orch()
	if !o.TryAcquire() {
		t.Fatal("stand should be free")
	}
	o.ExecuteRun(run, scenario(10, 60, iv("f1"), iv("f2")))
	if w.state("r1") != schema.StateCompleted || w.outcomes["r1"] != schema.OutcomeOriginalFailure {
		t.Fatalf("state=%s outcome=%q", w.state("r1"), w.outcomes["r1"])
	}
	if !o.TryAcquire() {
		t.Fatal("ExecuteRun must release the stand when it finishes")
	}
	if o.Cancel("r1") {
		t.Fatal("Cancel of a finished run must report false")
	}
}

func TestExecuteRun_CancelWhileSUTBlocks_ResultsInCancelledNeverPass(t *testing.T) {
	w := newWorld()
	entered := make(chan struct{})
	w.checkout = func(ctx context.Context, _ string, _ []string) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}
	run := w.addRun("r1", schema.StateValidating, []string{"f1"})
	o := w.orch()
	o.TryAcquire()
	done := make(chan struct{})
	go func() { o.ExecuteRun(run, scenario(10, 60, iv("f1"))); close(done) }()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("SUT never entered")
	}
	if !o.Cancel("r1") {
		t.Fatal("Cancel of an in-flight run must report true")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ExecuteRun did not return after Cancel")
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.runs["r1"].State != schema.StateCancelled {
		t.Fatalf("state=%s, want CANCELLED", w.runs["r1"].State)
	}
	if !hasState(w.updateStateArgs["r1"], schema.StateCancelled) {
		t.Fatalf("UpdateState was never called with CANCELLED: %v", w.updateStateArgs["r1"])
	}
	if o, ok := w.outcomes["r1"]; ok {
		t.Fatalf("a cancelled run must have no outcome, got %q", o)
	}
	if _, ok := w.infra["r1"]; ok {
		t.Fatal("cancellation is not an infra error")
	}
	if w.oracleCalls != 0 {
		t.Fatal("oracle must not run for a cancelled run")
	}
	if w.deactivateCount["r1"] != 1 {
		t.Fatalf("proxy scope must be released exactly once, got %d", w.deactivateCount["r1"])
	}
	if o2 := o; !o2.TryAcquire() {
		t.Fatal("stand must be released after cancellation")
	}
}

func hasState(states []schema.RunState, s schema.RunState) bool {
	for _, x := range states {
		if x == s {
			return true
		}
	}
	return false
}

func TestExecuteRun_InfraError_SetInfraErrorNotOutcome(t *testing.T) {
	w := newWorld()
	w.resetErr = func(int) error { return errors.New("compose up failed") }
	run := w.addRun("r1", schema.StateValidating, []string{"f1"})
	o := w.orch()
	o.TryAcquire()
	o.ExecuteRun(run, scenario(10, 60, iv("f1")))
	if !strings.Contains(w.infra["r1"], "compose up failed") {
		t.Fatalf("infra reason=%q", w.infra["r1"])
	}
	if _, ok := w.outcomes["r1"]; ok {
		t.Fatal("an infra error must not produce an outcome")
	}
	if w.state("r1") != schema.StateInfraError {
		t.Fatalf("state=%s", w.state("r1"))
	}
	if w.checkoutCalls != 0 || len(w.activateCount) != 0 {
		t.Fatal("nothing may run after a failed reset")
	}
}

func TestCancel_UnknownRunReturnsFalse(t *testing.T) {
	o := newWorld().orch()
	if o.Cancel("nope") || o.Cancel("") {
		t.Fatal("Cancel of an unknown run id must report false")
	}
}

func TestTryAcquire_IsExclusive(t *testing.T) {
	o := newWorld().orch()
	if !o.TryAcquire() || o.TryAcquire() {
		t.Fatal("second TryAcquire while held must fail")
	}
	o.Release()
	if !o.TryAcquire() {
		t.Fatal("TryAcquire after Release must succeed")
	}
}

// ---------------------------------------------------------------------------
// Orchestrator.Reduce
// ---------------------------------------------------------------------------

func parentRun(w *world, ids ...string) *schema.Run {
	r := w.addRun("run-1", schema.StateCompleted, ids)
	w.outcomes["run-1"] = schema.OutcomeOriginalFailure
	return r
}

func TestReduce_FindsRequiredFault_ResetsBeforeEveryCandidate(t *testing.T) {
	for _, algo := range []reducer.Algorithm{reducer.AlgoDDMin, reducer.AlgoSequential, reducer.AlgoDependencyAware} {
		t.Run(string(algo), func(t *testing.T) {
			w := newWorld()
			w.requireFault("fault-1")
			parent := parentRun(w, "fault-1", "fault-2")
			sc := scenario(100, 600, iv("fault-1"), iv("fault-2"))
			rec, err := w.orch().Reduce(context.Background(), parent, sc, algo)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(rec.Accepted, []string{"fault-1"}) {
				t.Fatalf("Accepted=%v, want [fault-1]", rec.Accepted)
			}
			if !rec.Confirmed || !rec.OneMinimal || rec.StopReason != "minimal" || rec.Unstable {
				t.Fatalf("%+v", rec)
			}
			if w.resetCalls != rec.RunsUsed {
				t.Fatalf("Reset called %d times but RunsUsed=%d (FR02: every candidate resets first)", w.resetCalls, rec.RunsUsed)
			}
			if len(w.candidateRuns) != rec.RunsUsed {
				t.Fatalf("candidate runs=%d RunsUsed=%d", len(w.candidateRuns), rec.RunsUsed)
			}
			if w.checkoutCalls != rec.RunsUsed || w.oracleCalls != rec.RunsUsed {
				t.Fatalf("checkout=%d oracle=%d RunsUsed=%d", w.checkoutCalls, w.oracleCalls, rec.RunsUsed)
			}
			// Per-candidate order: create, RESETTING, reset, READY, activate ... (never activate before reset).
			log := w.logCopy()
			for i, l := range log {
				if l == "activate" && (i < 2 || log[i-1] != "state:READY" || log[i-2] != "reset") {
					t.Fatalf("activate at %d not preceded by reset -> READY: %v", i, log[max(0, i-3):i+1])
				}
			}
			if rec.ConfirmationRuns == 0 || rec.ConfirmationRuns >= rec.RunsUsed {
				t.Fatalf("ConfirmationRuns=%d RunsUsed=%d", rec.ConfirmationRuns, rec.RunsUsed)
			}
			// Persistence: RUNNING first, COMPLETED last, same reduction id.
			if len(w.reductions) != 2 {
				t.Fatalf("SaveReduction called %d times, want 2", len(w.reductions))
			}
			first, last := w.reductions[0], w.reductions[1]
			if first.State != "RUNNING" || last.State != "COMPLETED" || first.ReductionID != last.ReductionID {
				t.Fatalf("first=%+v last=%+v", first, last)
			}
			if last.RunsUsed != rec.RunsUsed || !reflect.DeepEqual(last.Accepted, []string{"fault-1"}) {
				t.Fatalf("persisted result differs: %+v", last)
			}
			// Candidate runs end COMPLETED and never in the middle of the state machine.
			for _, id := range w.candidateRuns {
				if st := w.state(id); st != schema.StateCompleted {
					t.Fatalf("candidate %s ended in %s", id, st)
				}
			}
		})
	}
}

// Regression: candidate run ids used to be "<parent>-c<seq>", so a second reduction of
// the same run reused terminal candidate runs and failed with
// "illegal transition COMPLETED -> RESETTING".
func TestReduce_TwiceOnSameParent_CandidateIDsUniqueAndBothSucceed(t *testing.T) {
	w := newWorld() // CreateRun fails on a duplicate run_id, like the real primary key
	w.requireFault("fault-1")
	parent := parentRun(w, "fault-1", "fault-2")
	sc := scenario(100, 600, iv("fault-1"), iv("fault-2"))
	o := w.orch()

	rec1, err := o.Reduce(context.Background(), parent, sc, reducer.AlgoDDMin)
	if err != nil {
		t.Fatal(err)
	}
	n1 := len(w.candidateRuns)
	rec2, err := o.Reduce(context.Background(), parent, sc, reducer.AlgoDDMin)
	if err != nil {
		t.Fatalf("second reduction of the same run failed: %v", err)
	}
	for i, rec := range []*schema.Reduction{rec1, rec2} {
		if rec.StopReason != "minimal" || !reflect.DeepEqual(rec.Accepted, []string{"fault-1"}) || !rec.Confirmed {
			t.Fatalf("reduction %d: %+v", i+1, rec)
		}
	}
	if rec1.ReductionID == rec2.ReductionID {
		t.Fatal("reduction ids must differ")
	}
	seen := map[string]bool{}
	for _, id := range w.candidateRuns {
		if seen[id] {
			t.Fatalf("candidate run id %s reused", id)
		}
		seen[id] = true
	}
	if len(w.candidateRuns) != n1+rec2.RunsUsed || n1 != rec1.RunsUsed {
		t.Fatalf("candidate runs=%d first=%d second=%d", len(w.candidateRuns), rec1.RunsUsed, rec2.RunsUsed)
	}
	if o.Cancel(parent.RunID) {
		t.Fatal("finished reductions must be unregistered")
	}
}

func TestReduce_CancelDuringReduce_StopsAndKeepsLastConfirmed(t *testing.T) {
	// Schedule (required=fault-1, two interventions): calls 1-4 original (+3 confirm),
	// 5 screens [fault-1], 6-8 confirm it, 9 probes the empty set.
	tests := []struct {
		cancelAt int
		want     []string
		confd    bool
	}{
		{1, []string{"fault-1", "fault-2"}, false},
		{6, []string{"fault-1", "fault-2"}, true}, // mid-confirmation of [fault-1]: NOT accepted
		{9, []string{"fault-1"}, true},            // last confirmed set survives
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("cancelAt=%d", tc.cancelAt), func(t *testing.T) {
			w := newWorld()
			w.requireFault("fault-1")
			parent := parentRun(w, "fault-1", "fault-2")
			o := w.orch()
			w.onReset = func(n int) {
				if n == tc.cancelAt {
					if !o.Cancel(parent.RunID) {
						t.Error("Cancel(parent) must find the in-flight reduction")
					}
				}
			}
			sc := scenario(100, 600, iv("fault-1"), iv("fault-2"))
			rec, err := o.Reduce(context.Background(), parent, sc, reducer.AlgoDDMin)
			if err != nil {
				t.Fatalf("cancellation is reported in the record, not as an error: %v", err)
			}
			if rec.StopReason != "CANCELLED" || rec.OneMinimal {
				t.Fatalf("StopReason=%s OneMinimal=%v", rec.StopReason, rec.OneMinimal)
			}
			if !reflect.DeepEqual(rec.Accepted, tc.want) || rec.Confirmed != tc.confd {
				t.Fatalf("Accepted=%v Confirmed=%v, want %v/%v", rec.Accepted, rec.Confirmed, tc.want, tc.confd)
			}
			if w.resetCalls != tc.cancelAt {
				t.Fatalf("Reset called %d times, want %d (no candidate may start after cancel)", w.resetCalls, tc.cancelAt)
			}
			// The interrupted candidate is CANCELLED and has no outcome (never PASS).
			lastID := w.candidateRuns[len(w.candidateRuns)-1]
			if st := w.state(lastID); st != schema.StateCancelled {
				t.Fatalf("interrupted candidate state=%s, want CANCELLED", st)
			}
			if _, ok := w.outcomes[lastID]; ok {
				t.Fatalf("interrupted candidate has outcome %q", w.outcomes[lastID])
			}
			for _, id := range w.candidateRuns {
				if w.deactivateCount[id] != 1 {
					t.Fatalf("candidate %s: Deactivate called %d times", id, w.deactivateCount[id])
				}
			}
			if last := w.reductions[len(w.reductions)-1]; last.State != "COMPLETED" || last.StopReason != "CANCELLED" {
				t.Fatalf("persisted record: %+v", last)
			}
			if o.Cancel(parent.RunID) {
				t.Fatal("cancel handle must be released after Reduce returns")
			}
		})
	}
}

func TestReduce_BudgetClampedTo100RunsAnd600Seconds(t *testing.T) {
	w := newWorld()
	w.requireFault("fault-1")
	parent := parentRun(w, "fault-1", "fault-2")
	rec, err := w.orch().Reduce(context.Background(), parent, scenario(1000, 3600, iv("fault-1"), iv("fault-2")), reducer.AlgoDDMin)
	if err != nil {
		t.Fatal(err)
	}
	if rec.MaxRuns != 100 || rec.MaxSeconds != 600 {
		t.Fatalf("MaxRuns=%d MaxSeconds=%d, want 100/600", rec.MaxRuns, rec.MaxSeconds)
	}
	if w.reductions[0].MaxRuns != 100 || w.reductions[0].MaxSeconds != 600 {
		t.Fatalf("persisted RUNNING record: %+v", w.reductions[0])
	}
}

func TestReduce_ScenarioBudgetBelowCeilingIsHonoured(t *testing.T) {
	w := newWorld()
	w.requireFault("fault-1")
	parent := parentRun(w, "fault-1", "fault-2")
	rec, err := w.orch().Reduce(context.Background(), parent, scenario(5, 30, iv("fault-1"), iv("fault-2")), reducer.AlgoDDMin)
	if err != nil {
		t.Fatal(err)
	}
	if rec.MaxRuns != 5 || rec.MaxSeconds != 30 {
		t.Fatalf("MaxRuns=%d MaxSeconds=%d", rec.MaxRuns, rec.MaxSeconds)
	}
	if rec.StopReason != "BUDGET_EXHAUSTED_RUNS" || rec.RunsUsed != 5 || w.resetCalls != 5 {
		t.Fatalf("StopReason=%s RunsUsed=%d resets=%d", rec.StopReason, rec.RunsUsed, w.resetCalls)
	}
	if rec.OneMinimal {
		t.Fatal("a budget stop must not claim minimality")
	}
}

func TestReduce_DependsOnNeverViolatedByCandidates(t *testing.T) {
	// fault-2 is the required one but declares fault-1 as a prerequisite: [fault-2]
	// alone must never be run, so the result is [fault-1 fault-2].
	for _, algo := range []reducer.Algorithm{reducer.AlgoDDMin, reducer.AlgoSequential, reducer.AlgoDependencyAware} {
		t.Run(string(algo), func(t *testing.T) {
			w := newWorld()
			w.requireFault("fault-2")
			parent := parentRun(w, "fault-1", "fault-2", "fault-3")
			sc := scenario(100, 600, iv("fault-1"), iv("fault-2", "fault-1"), iv("fault-3"))
			rec, err := w.orch().Reduce(context.Background(), parent, sc, algo)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(sortedCopy(rec.Accepted), []string{"fault-1", "fault-2"}) {
				t.Fatalf("Accepted=%v", rec.Accepted)
			}
			if rec.SkippedInvalid == 0 {
				t.Fatal("expected invalid candidates to be skipped")
			}
			for run, act := range w.active {
				if hasID(act, "fault-2") && !hasID(act, "fault-1") {
					t.Fatalf("run %s activated fault-2 without its prerequisite: %v", run, act)
				}
			}
			if w.resetCalls != rec.RunsUsed {
				t.Fatalf("resets=%d RunsUsed=%d", w.resetCalls, rec.RunsUsed)
			}
		})
	}
}

func TestReduce_OriginalNotReproduced_StopsWithOneRun(t *testing.T) {
	w := newWorld() // default fake: invariant always holds -> PASS
	parent := parentRun(w, "fault-1", "fault-2")
	rec, err := w.orch().Reduce(context.Background(), parent, scenario(100, 600, iv("fault-1"), iv("fault-2")), reducer.AlgoDDMin)
	if err != nil {
		t.Fatal(err)
	}
	if rec.StopReason != "ORIGINAL_NOT_REPRODUCED" || rec.RunsUsed != 1 || rec.Confirmed {
		t.Fatalf("%+v", rec)
	}
	if !reflect.DeepEqual(rec.Accepted, []string{"fault-1", "fault-2"}) {
		t.Fatalf("Accepted=%v", rec.Accepted)
	}
}

func TestReduce_ResetFailureMidReduction_IsInfraErrorKeepingLastConfirmed(t *testing.T) {
	w := newWorld()
	w.requireFault("fault-1")
	w.resetErr = func(n int) error {
		if n == 9 {
			return errors.New("compose reset failed")
		}
		return nil
	}
	parent := parentRun(w, "fault-1", "fault-2")
	rec, err := w.orch().Reduce(context.Background(), parent, scenario(100, 600, iv("fault-1"), iv("fault-2")), reducer.AlgoDDMin)
	if err != nil {
		t.Fatal(err)
	}
	if rec.StopReason != "INFRA_ERROR" || !strings.Contains(rec.Error, "compose reset failed") {
		t.Fatalf("StopReason=%s Error=%q", rec.StopReason, rec.Error)
	}
	if !reflect.DeepEqual(rec.Accepted, []string{"fault-1"}) || !rec.Confirmed {
		t.Fatalf("Accepted=%v Confirmed=%v", rec.Accepted, rec.Confirmed)
	}
	// FR02: the failed reset aborted that candidate before anything touched the proxy/SUT/oracle.
	if w.checkoutCalls != 8 || w.oracleCalls != 8 || w.resetCalls != 9 {
		t.Fatalf("resets=%d checkouts=%d oracle=%d", w.resetCalls, w.checkoutCalls, w.oracleCalls)
	}
	lastID := w.candidateRuns[len(w.candidateRuns)-1]
	if w.state(lastID) != schema.StateInfraError || w.infra[lastID] == "" {
		t.Fatalf("failed candidate state=%s infra=%q", w.state(lastID), w.infra[lastID])
	}
}

func sortedCopy(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

// candidates.accepted / confirmations: the reducer's per-candidate verdicts reach the store,
// addressed by reduction id, for the original set and for every confirmed candidate.
func TestReduce_PersistsCandidateDecisions(t *testing.T) {
	w := newWorld()
	w.requireFault("fault-1")
	parent := parentRun(w, "fault-1", "fault-2")
	sc := scenario(100, 600, iv("fault-1"), iv("fault-2"))
	rec, err := w.orch().Reduce(context.Background(), parent, sc, reducer.AlgoDDMin)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.decisions) < 2 {
		t.Fatalf("want original + accepted candidate decisions, got %+v", w.decisions)
	}
	var accepted []decisionRec
	for _, d := range w.decisions {
		if d.reductionID != rec.ReductionID {
			t.Fatalf("decision for another reduction: %+v", d)
		}
		if d.accepted {
			if d.confirmations != reducer.ConfirmationRuns {
				t.Fatalf("an accepted candidate must have %d confirmations: %+v", reducer.ConfirmationRuns, d)
			}
			accepted = append(accepted, d)
		}
	}
	if got := accepted[len(accepted)-1].ids; !reflect.DeepEqual(got, rec.Accepted) {
		t.Fatalf("the last accepted candidate %v must equal the reduction result %v", got, rec.Accepted)
	}
}
