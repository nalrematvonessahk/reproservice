package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"reproservice/internal/schema"
	"reproservice/src/controller/store"
	"reproservice/src/oracle"
	"reproservice/src/reducer"
	"reproservice/src/runner"
)

// Orchestrator drives the run state machine described in docs/07-architecture.md,
// composing the runner (reset/health), proxy (fault injection scoping), the demo SUT
// (via SUTClient) and the oracle (invariant check). Only one run may occupy the stand
// at a time -- NFR: a concurrent run against a busy stand gets 409.
type Orchestrator struct {
	store   *store.Store
	runner  *runner.Runner
	oracle  *oracle.Oracle
	proxy   *ProxyClient
	sut     *SUTClient
	busy    atomic.Bool

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func NewOrchestrator(st *store.Store, rn *runner.Runner, or *oracle.Oracle, px *ProxyClient, sut *SUTClient) *Orchestrator {
	return &Orchestrator{store: st, runner: rn, oracle: or, proxy: px, sut: sut, cancels: map[string]context.CancelFunc{}}
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

// Cancel stops new work for an in-flight run; FR09 requires this to prevent new
// candidates and result in CANCELLED, never PASS.
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
	o.mu.Lock()
	o.cancels[run.RunID] = cancel
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		delete(o.cancels, run.RunID)
		o.mu.Unlock()
		cancel()
	}()

	outcome, err := o.executeOnce(ctx, run.RunID, sc, run.ActiveIDs)
	finalCtx := context.Background() // the run's own ctx may already be cancelled
	if err != nil {
		if errors.Is(err, context.Canceled) {
			o.store.UpdateState(finalCtx, run.RunID, schema.StateCancelled)
			return
		}
		log.Printf("orchestrator: run %s: %v", run.RunID, err)
		o.store.SetInfraError(finalCtx, run.RunID, err.Error())
		return
	}
	if err := o.store.SetOutcome(finalCtx, run.RunID, outcome, ""); err != nil {
		log.Printf("orchestrator: run %s: persist outcome: %v", run.RunID, err)
	}
}

// executeOnce performs exactly one reset+run+assert cycle for the given intervention
// subset and returns the resulting Outcome. It is reused both for the top-level run
// and for every reduction candidate (docs/07-architecture.md step 4: "выполнить
// reset, сценарий и oracle" for each candidate).
func (o *Orchestrator) executeOnce(ctx context.Context, runID string, sc *schema.Scenario, activeIDs []string) (schema.Outcome, error) {
	if err := o.store.UpdateState(ctx, runID, schema.StateResetting); err != nil {
		return "", fmt.Errorf("-> RESETTING: %w", err)
	}
	if err := o.runner.Reset(ctx); err != nil {
		return "", fmt.Errorf("reset failed: %w", err)
	}

	if err := o.store.UpdateState(ctx, runID, schema.StateReady); err != nil {
		return "", fmt.Errorf("-> READY: %w", err)
	}

	active := interventionsByID(sc.Interventions, activeIDs)
	if err := o.proxy.Activate(runID, active); err != nil {
		return "", fmt.Errorf("activate proxy: %w", err)
	}
	defer o.proxy.Deactivate(runID)

	if err := o.store.UpdateState(ctx, runID, schema.StateRunning); err != nil {
		return "", fmt.Errorf("-> RUNNING: %w", err)
	}

	orderID := runID // one order per run keeps the oracle query unambiguous
	checkoutErr := o.runCheckoutWithTimeout(ctx, orderID, logicalOperationOf(sc), runID)

	if err := o.store.UpdateState(ctx, runID, schema.StateAsserting); err != nil {
		return "", fmt.Errorf("-> ASSERTING: %w", err)
	}

	assertion, err := o.oracle.Check(ctx, sc.InvariantID, orderID)
	if err != nil {
		return "", fmt.Errorf("oracle check: %w", err)
	}
	o.store.InsertAssertion(ctx, runID, assertion)

	if assertion.InfraError {
		return "", fmt.Errorf("oracle reported an infra error, not a verdict")
	}
	if checkoutErr != nil && !assertion.Violated {
		// The checkout call itself failed for a reason unrelated to the invariant under
		// test (e.g. inventory down) -- that is a different failure, not the one we're
		// trying to reproduce.
		return schema.OutcomeOtherFailure, nil
	}
	if assertion.Violated {
		return schema.OutcomeOriginalFailure, nil
	}
	return schema.OutcomePass, nil
}

func (o *Orchestrator) runCheckoutWithTimeout(ctx context.Context, orderID, logicalOperation, runID string) error {
	done := make(chan error, 1)
	go func() {
		_, err := o.sut.Checkout(orderID, logicalOperation, runID)
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(15 * time.Second):
		return fmt.Errorf("checkout timed out")
	case <-ctx.Done():
		return ctx.Err()
	}
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

// candidateExecutor adapts the orchestrator to reducer.Executor for one reduction run.
type candidateExecutor struct {
	o            *Orchestrator
	ctx          context.Context
	scenario     *schema.Scenario
	versionID    string
	parentRunID  string
	reductionID  string
	seq          int
}

func (c *candidateExecutor) Execute(ctx context.Context, interventionIDs []string) (reducer.Outcome, error) {
	c.seq++
	candidateRunID := fmt.Sprintf("%s-cand-%d", c.parentRunID, c.seq)
	run := &schema.Run{
		RunID: candidateRunID, ScenarioID: c.scenario.ID, VersionID: c.versionID,
		IdempotencyKey: candidateRunID, State: schema.StateValidating, ActiveIDs: interventionIDs,
	}
	if _, err := c.o.store.CreateRun(ctx, run); err != nil {
		return "", err
	}
	outcome, err := c.o.executeOnce(ctx, candidateRunID, c.scenario, interventionIDs)
	if err != nil {
		c.o.store.SetInfraError(ctx, candidateRunID, err.Error())
		return "", err
	}
	c.o.store.SetOutcome(ctx, candidateRunID, outcome, "")
	c.o.store.InsertCandidate(ctx, candidateRunID, c.reductionID, c.parentRunID, interventionIDs, outcome, false, 0)
	return reducer.Outcome(outcome), nil
}
