package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"reproservice/internal/schema"
	"reproservice/src/oracle"
	"reproservice/src/reducer"
	"reproservice/src/runner"
)

// The orchestrator depends on these narrow interfaces so the lifecycle, classification
// and reduction logic can be unit-tested without Docker. The concrete store, runner,
// oracle, ProxyClient and SUTClient satisfy them.
type runStore interface {
	CreateRun(ctx context.Context, run *schema.Run) (bool, error)
	UpdateState(ctx context.Context, runID string, to schema.RunState) error
	SetOutcome(ctx context.Context, runID string, outcome schema.Outcome, stopReason string) error
	SetInfraError(ctx context.Context, runID, reason string) error
	InsertAssertion(ctx context.Context, runID string, a schema.Assertion) error
	InsertCandidate(ctx context.Context, candidateID, reductionID, runID string, interventionIDs []string, outcome schema.Outcome, accepted bool, confirmations int) error
	SaveReduction(ctx context.Context, r schema.Reduction) error
}

type resetter interface {
	Reset(ctx context.Context) error
}

type invariantChecker interface {
	Check(ctx context.Context, invariantID, orderID string) (schema.Assertion, error)
}

type proxyAPI interface {
	Activate(runID string, interventions []schema.Intervention) error
	Deactivate(runID string) (map[string]int, error)
}

type sutAPI interface {
	Checkout(ctx context.Context, orderID, logicalOperation, runID string) (*CheckoutResult, error)
}

var (
	_ resetter         = (*runner.Runner)(nil)
	_ invariantChecker = (*oracle.Oracle)(nil)
	_ proxyAPI         = (*ProxyClient)(nil)
	_ sutAPI           = (*SUTClient)(nil)
)

// defaultCheckoutTimeout bounds one execution of the scenario's business operation;
// exceeding it yields TIMEOUT (never ORIGINAL_FAILURE).
const defaultCheckoutTimeout = 15 * time.Second

// Orchestrator drives the run state machine described in docs/07-architecture.md,
// composing the runner (reset/health), proxy (fault injection scoping), the demo SUT
// (via SUTClient) and the oracle (invariant check). Only one run may occupy the stand
// at a time -- NFR: a concurrent run against a busy stand gets 409.
type Orchestrator struct {
	store  runStore
	runner resetter
	oracle invariantChecker
	proxy  proxyAPI
	sut    sutAPI
	busy   atomic.Bool

	checkoutTimeout time.Duration

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func NewOrchestrator(st runStore, rn resetter, or invariantChecker, px proxyAPI, sut sutAPI) *Orchestrator {
	return &Orchestrator{store: st, runner: rn, oracle: or, proxy: px, sut: sut,
		checkoutTimeout: defaultCheckoutTimeout, cancels: map[string]context.CancelFunc{}}
}

// TryAcquire atomically claims the stand for the caller, or reports false if another
// run already holds it. The API layer calls this synchronously -- before creating a
// run row or returning a response -- so two concurrent POST /api/runs cannot both
// receive 202 (a non-atomic "check then decide later" sequence would race, since
// ExecuteRun's own acquisition happens in a separate goroutine after the HTTP
// response has already been written).
func (o *Orchestrator) TryAcquire() bool { return o.busy.CompareAndSwap(false, true) }

// Release gives the stand back up. Callers that fail to start a run after a
// successful TryAcquire (e.g. the DB insert failed) must call this themselves;
// ExecuteRun releases on its own once the run it was given finishes.
func (o *Orchestrator) Release() { o.busy.Store(false) }

// registerCancel makes id cancellable via Cancel until the returned func is called.
func (o *Orchestrator) registerCancel(id string, cancel context.CancelFunc) (unregister func()) {
	o.mu.Lock()
	o.cancels[id] = cancel
	o.mu.Unlock()
	return func() {
		o.mu.Lock()
		delete(o.cancels, id)
		o.mu.Unlock()
		cancel()
	}
}

// Cancel stops new work for an in-flight run or reduction; FR09 requires this to
// prevent new candidates and result in CANCELLED, never PASS. It reports false when
// nothing with that ID is currently in flight (already finished or never started).
func (o *Orchestrator) Cancel(runID string) bool {
	o.mu.Lock()
	cancel, ok := o.cancels[runID]
	o.mu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}

// ExecuteRun drives a top-level run (the one returned to the API caller) through
// RESETTING -> READY -> RUNNING -> ASSERTING -> COMPLETED/INFRA_ERROR/CANCELLED. It
// runs in its own goroutine; callers observe progress via GET /api/runs/{id}.
// The caller must already hold the stand (via a successful TryAcquire) before
// starting this goroutine; ExecuteRun releases it when the run finishes.
func (o *Orchestrator) ExecuteRun(run *schema.Run, sc *schema.Scenario) {
	defer o.Release()

	ctx, cancel := context.WithCancel(context.Background())
	defer o.registerCancel(run.RunID, cancel)()

	outcome, detail, err := o.executeOnce(ctx, run.RunID, sc, run.ActiveIDs)
	finalCtx := context.Background() // the run's own ctx may already be cancelled
	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			o.store.UpdateState(finalCtx, run.RunID, schema.StateCancelled)
			return
		}
		log.Printf("orchestrator: run %s: %v", run.RunID, err)
		o.store.SetInfraError(finalCtx, run.RunID, err.Error())
		return
	}
	if err := o.store.SetOutcome(finalCtx, run.RunID, outcome, detail); err != nil {
		log.Printf("orchestrator: run %s: persist outcome: %v", run.RunID, err)
	}
}

// executeOnce performs exactly one reset+run+assert cycle for the given intervention
// subset and returns the resulting Outcome (plus a human-readable detail for
// non-obvious outcomes). It is reused both for the top-level run and for every
// reduction candidate (docs/07-architecture.md step 4: "выполнить reset, сценарий и
// oracle" for each candidate).
//
// Any returned error is an infrastructure/cancellation problem and must never be
// interpreted as a verdict about the bug under test.
func (o *Orchestrator) executeOnce(ctx context.Context, runID string, sc *schema.Scenario, activeIDs []string) (schema.Outcome, string, error) {
	if err := o.store.UpdateState(ctx, runID, schema.StateResetting); err != nil {
		return "", "", fmt.Errorf("-> RESETTING: %w", err)
	}
	// FR02: a failed reset aborts here -- no proxy activation, no checkout, no oracle.
	if err := o.runner.Reset(ctx); err != nil {
		return "", "", fmt.Errorf("reset failed: %w", err)
	}

	if err := o.store.UpdateState(ctx, runID, schema.StateReady); err != nil {
		return "", "", fmt.Errorf("-> READY: %w", err)
	}

	active := interventionsByID(sc.Interventions, activeIDs)
	if err := o.proxy.Activate(runID, active); err != nil {
		return "", "", fmt.Errorf("activate proxy: %w", err)
	}
	deactivated := false
	defer func() {
		if !deactivated {
			o.proxy.Deactivate(runID) // safety net on early returns; counts not needed here
		}
	}()

	if err := o.store.UpdateState(ctx, runID, schema.StateRunning); err != nil {
		return "", "", fmt.Errorf("-> RUNNING: %w", err)
	}

	orderID := runID // one order per run keeps the oracle query unambiguous
	timedOut, checkoutErr := o.runCheckout(ctx, orderID, logicalOperationOf(sc), runID)
	if err := ctx.Err(); err != nil {
		return "", "", err // cancelled while the scenario was running
	}

	fired, err := o.proxy.Deactivate(runID)
	deactivated = true
	if err != nil {
		return "", "", fmt.Errorf("collect intervention matches: %w", err)
	}

	if err := o.store.UpdateState(ctx, runID, schema.StateAsserting); err != nil {
		return "", "", fmt.Errorf("-> ASSERTING: %w", err)
	}

	assertion, err := o.oracle.Check(ctx, sc.InvariantID, orderID)
	if err != nil {
		return "", "", fmt.Errorf("oracle check: %w", err)
	}
	o.store.InsertAssertion(ctx, runID, assertion)
	if assertion.InfraError {
		return "", "", fmt.Errorf("oracle reported an infra error, not a verdict")
	}

	outcome, detail := classify(checkoutErr, timedOut, assertion, activeIDs, fired)
	return outcome, detail, nil
}

// classify maps one observed execution to an Outcome. Precedence matters and is part
// of the contract (docs/07-architecture.md, FR03/FR06):
//
//  1. TIMEOUT          -- the operation did not finish in time; this is never the
//     original failure, even if a duplicate charge happened to land (the verification
//     the original failure requires was not completed within the window).
//  2. INAPPLICABLE     -- an activated intervention matched zero or several events, so
//     the scenario did not execute as defined (FR03).
//  3. ORIGINAL_FAILURE -- the oracle verified the business invariant was violated the
//     original way (duplicate charge).
//  4. OTHER_FAILURE    -- the operation failed, or the invariant was violated some other
//     way (e.g. no charge at all); not the bug being reproduced.
//  5. PASS             -- operation succeeded and the invariant holds.
func classify(checkoutErr error, timedOut bool, a schema.Assertion, activeIDs []string, fired map[string]int) (schema.Outcome, string) {
	if timedOut {
		return schema.OutcomeTimeout, "checkout did not complete within the timeout"
	}
	ids := append([]string{}, activeIDs...)
	sort.Strings(ids)
	for _, id := range ids {
		if n := fired[id]; n != 1 {
			return schema.OutcomeInapplicable, fmt.Sprintf("intervention %s matched %d events (expected exactly 1)", id, n)
		}
	}
	if a.Violated && a.Kind != schema.ViolationMissing {
		return schema.OutcomeOriginalFailure, fmt.Sprintf("%s violated: expected %s, actual %s", a.InvariantID, a.Expected, a.Actual)
	}
	if a.Violated {
		return schema.OutcomeOtherFailure, fmt.Sprintf("%s violated differently from the original failure (%s): expected %s, actual %s", a.InvariantID, a.Kind, a.Expected, a.Actual)
	}
	if checkoutErr != nil {
		// The checkout call itself failed for a reason unrelated to the invariant under
		// test (e.g. inventory down) -- a different failure, not the one being reproduced.
		return schema.OutcomeOtherFailure, "checkout failed: " + checkoutErr.Error()
	}
	return schema.OutcomePass, ""
}

// runCheckout runs the scenario's business operation under checkoutTimeout. timedOut
// is true only when the timeout (not a parent cancellation) ended the call. The
// request context is cancelled on return so no request outlives the run.
func (o *Orchestrator) runCheckout(ctx context.Context, orderID, logicalOperation, runID string) (timedOut bool, err error) {
	cctx, cancel := context.WithTimeout(ctx, o.checkoutTimeout)
	defer cancel()
	_, err = o.sut.Checkout(cctx, orderID, logicalOperation, runID)
	if err == nil {
		return false, nil
	}
	if ctx.Err() != nil {
		return false, err // parent cancelled: the caller handles cancellation
	}
	return isTimeout(err) || errors.Is(cctx.Err(), context.DeadlineExceeded), err
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// logicalOperationOf returns the logical_operation shared by a scenario's
// interventions. This demo's scenarios all target one business operation per
// scenario, so the first intervention's value is authoritative.
func logicalOperationOf(sc *schema.Scenario) string {
	if len(sc.Interventions) == 0 {
		return sc.ID
	}
	return sc.Interventions[0].Selector.LogicalOperation
}

func interventionsByID(all []schema.Intervention, ids []string) []schema.Intervention {
	set := map[string]bool{}
	for _, id := range ids {
		set[id] = true
	}
	out := make([]schema.Intervention, 0, len(ids))
	for _, iv := range all {
		if set[iv.ID] {
			out = append(out, iv)
		}
	}
	return out
}

// validCandidate implements the shared explicit-prerequisite check (ADR-005): a
// candidate is valid only if every intervention it contains has all of its declared
// depends_on prerequisites in the candidate too. It is applied identically to all
// algorithms; observed trace links are never used here.
func validCandidate(sc *schema.Scenario) func([]string) bool {
	deps := map[string][]string{}
	for _, iv := range sc.Interventions {
		deps[iv.ID] = iv.DependsOn
	}
	return func(ids []string) bool {
		in := make(map[string]bool, len(ids))
		for _, id := range ids {
			in[id] = true
		}
		for _, id := range ids {
			for _, d := range deps[id] {
				if !in[d] {
					return false
				}
			}
		}
		return true
	}
}

// Reduce runs one reduction of parent's interventions with the chosen algorithm and
// persists the outcome (FR08). The caller must hold the stand (TryAcquire). While it
// runs, Cancel(parent.RunID) stops further candidates (FR09); the best candidate
// confirmed so far is still reported, with stop_reason CANCELLED.
func (o *Orchestrator) Reduce(ctx context.Context, parent *schema.Run, sc *schema.Scenario, algo reducer.Algorithm) (*schema.Reduction, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer o.registerCancel(parent.RunID, cancel)()

	budget := reducer.Budget{MaxRuns: sc.Budget.Runs, MaxDuration: time.Duration(sc.Budget.Seconds) * time.Second}.Clamp()
	rec := schema.Reduction{
		ReductionID: newID("red"), RunID: parent.RunID, Algorithm: string(algo), State: "RUNNING",
		Accepted: parent.ActiveIDs, MaxRuns: budget.MaxRuns, MaxSeconds: int(budget.MaxDuration / time.Second),
	}
	if err := o.store.SaveReduction(ctx, rec); err != nil {
		return nil, fmt.Errorf("persist reduction: %w", err)
	}

	opts := reducer.Options{Valid: validCandidate(sc)}
	if algo == reducer.AlgoDependencyAware {
		deps := map[string][]string{}
		for _, iv := range sc.Interventions {
			deps[iv.ID] = iv.DependsOn
		}
		opts.Order = reducer.LeafFirstOrder(deps)
	}

	exec := &candidateExecutor{o: o, scenario: sc, versionID: parent.VersionID, parentRunID: parent.RunID, reductionID: rec.ReductionID}
	res, err := reducer.ReduceWithOptions(ctx, exec, parent.ActiveIDs, algo, budget, opts)
	if err != nil {
		return nil, err
	}

	rec.State = "COMPLETED"
	rec.Accepted = res.Accepted
	rec.Confirmed = res.Confirmed
	rec.OneMinimal = res.OneMinimal
	rec.Unstable = res.Unstable
	rec.RunsUsed = res.RunsUsed
	rec.ConfirmationRuns = res.ConfirmationRuns
	rec.CacheHits = res.CacheHits
	rec.SkippedInvalid = res.SkippedInvalid
	rec.StopReason = string(res.StopReason)
	rec.Error = res.Error
	rec.DurationMs = res.Duration.Milliseconds()
	if err := o.store.SaveReduction(context.Background(), rec); err != nil {
		log.Printf("orchestrator: reduction %s: persist result: %v", rec.ReductionID, err)
	}
	return &rec, nil
}

// candidateExecutor adapts the orchestrator to reducer.Executor for one reduction run.
type candidateExecutor struct {
	o           *Orchestrator
	scenario    *schema.Scenario
	versionID   string
	parentRunID string
	reductionID string
	seq         int
}

func (c *candidateExecutor) Execute(ctx context.Context, interventionIDs []string) (reducer.Outcome, error) {
	c.seq++
	// Unique per reduction: reducing the same run again must not collide with (and
	// reuse the terminal state of) candidate runs from an earlier reduction.
	candidateRunID := fmt.Sprintf("%s-%s-c%d", c.parentRunID, c.reductionID, c.seq)
	run := &schema.Run{
		RunID: candidateRunID, ScenarioID: c.scenario.ID, VersionID: c.versionID,
		IdempotencyKey: candidateRunID, State: schema.StateValidating, ActiveIDs: interventionIDs,
	}
	bg := context.Background()
	if _, err := c.o.store.CreateRun(bg, run); err != nil {
		return "", err
	}
	outcome, detail, err := c.o.executeOnce(ctx, candidateRunID, c.scenario, interventionIDs)
	if err != nil {
		if ctx.Err() != nil {
			// Cancelled or out of wall-clock budget mid-candidate: not an infra failure.
			c.o.store.UpdateState(bg, candidateRunID, schema.StateCancelled)
			return "", err
		}
		c.o.store.SetInfraError(bg, candidateRunID, err.Error())
		return "", err
	}
	c.o.store.SetOutcome(bg, candidateRunID, outcome, detail)
	c.o.store.InsertCandidate(bg, candidateRunID, c.reductionID, c.parentRunID, interventionIDs, outcome, false, 0)
	return reducer.Outcome(outcome), nil
}
