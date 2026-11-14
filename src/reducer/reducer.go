// Package reducer implements the intervention-shrinking algorithms described in
// docs/07-architecture.md "Сокращение" and ADR-003/ADR-005: a baseline ddmin, a
// sequential (one-at-a-time) removal baseline, and a dependency-aware variant that
// only changes candidate *ordering*, never acceptance criteria -- per ADR-005, a
// candidate is never rejected solely because of an observed dependency link.
//
// The package is deliberately decoupled from Docker/HTTP/Postgres: callers provide
// an Executor, which lets the algorithms be unit-tested without the real stand
// (src/reducer/reducer_test.go) and reused by the controller against the real one.
package reducer

import (
	"context"
	"fmt"
	"time"
)

// Outcome mirrors schema.Outcome without importing it, to keep this package
// dependency-free; the controller converts at the boundary.
type Outcome string

const (
	OutcomePass            Outcome = "PASS"
	OutcomeOriginalFailure Outcome = "ORIGINAL_FAILURE"
	OutcomeOtherFailure    Outcome = "OTHER_FAILURE"
	OutcomeTimeout         Outcome = "TIMEOUT"
	OutcomeInapplicable    Outcome = "INAPPLICABLE"
)

// Executor runs one candidate (a subset of the original intervention IDs) against a
// freshly reset stand and reports the outcome. Implementations must reset before
// every call (docs/07-architecture.md step 4) -- the reducer does not do this itself.
type Executor interface {
	Execute(ctx context.Context, interventionIDs []string) (Outcome, error)
}

type Budget struct {
	MaxRuns     int
	MaxDuration time.Duration
}

type StopReason string

const (
	StopMinimal         StopReason = "minimal"
	StopBudgetRuns      StopReason = "BUDGET_EXHAUSTED_RUNS"
	StopBudgetDuration  StopReason = "BUDGET_EXHAUSTED_SECONDS"
	StopCancelled       StopReason = "CANCELLED"
	StopUnstable        StopReason = "UNSTABLE_ORIGINAL"
)

type Result struct {
	Accepted    []string
	RunsUsed    int
	StopReason  StopReason
	Unstable    bool
	Algorithm   string
}

type budgetTracker struct {
	budget    Budget
	runsUsed  int
	deadline  time.Time
}

func newTracker(b Budget) *budgetTracker {
	return &budgetTracker{budget: b, deadline: time.Now().Add(b.MaxDuration)}
}

func (t *budgetTracker) exhausted() StopReason {
	if t.runsUsed >= t.budget.MaxRuns {
		return StopBudgetRuns
	}
	if time.Now().After(t.deadline) {
		return StopBudgetDuration
	}
	return ""
}

// run executes a candidate and counts it against the budget, including confirmation
// re-runs -- "Повторные подтверждения расходуют общий бюджет" (docs/07-architecture.md).
func (t *budgetTracker) run(ctx context.Context, exec Executor, ids []string) (Outcome, error) {
	t.runsUsed++
	return exec.Execute(ctx, ids)
}

// confirm re-runs a candidate 3 times and requires all 3 to reproduce the original
// failure (3/3) before it is accepted, per docs/07-architecture.md step 4.
func confirm(ctx context.Context, exec Executor, t *budgetTracker, ids []string) (ok bool, unstable bool, err error) {
	for i := 0; i < 3; i++ {
		if reason := t.exhausted(); reason != "" {
			return false, false, fmt.Errorf("budget exhausted during confirmation: %s", reason)
		}
		out, runErr := t.run(ctx, exec, ids)
		if runErr != nil {
			return false, false, runErr
		}
		if out != Outcome(OutcomeOriginalFailure) {
			return false, true, nil // fluctuation -> unstable, reject (docs: "кандидат отклоняется и помечается unstable")
		}
	}
	return true, false, nil
}

// Reduce validates the original failure on the full set, then delegates to the chosen
// algorithm. order is nil for the plain algorithms and a dependency-respecting
// candidate ordering for DependencyAware.
func Reduce(ctx context.Context, exec Executor, all []string, algo Algorithm, budget Budget, order func([]string) []string) (*Result, error) {
	t := newTracker(budget)

	out, err := t.run(ctx, exec, all)
	if err != nil {
		return nil, err
	}
	if out != Outcome(OutcomeOriginalFailure) {
		return &Result{Accepted: all, RunsUsed: t.runsUsed, StopReason: "ORIGINAL_NOT_REPRODUCED", Algorithm: string(algo)}, nil
	}

	switch algo {
	case AlgoSequential:
		return sequentialRemoval(ctx, exec, all, t)
	case AlgoDDMin:
		return ddmin(ctx, exec, all, t, nil)
	case AlgoDependencyAware:
		return ddmin(ctx, exec, all, t, order)
	default:
		return nil, fmt.Errorf("unknown algorithm %q", algo)
	}
}

type Algorithm string

const (
	AlgoDDMin           Algorithm = "ddmin"
	AlgoSequential       Algorithm = "sequential"
	AlgoDependencyAware  Algorithm = "dependency_aware"
)
