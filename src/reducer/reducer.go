// Package reducer implements the intervention-shrinking algorithms described in
// docs/07-architecture.md "Сокращение" and ADR-003/ADR-005: a baseline ddmin, a
// sequential (one-at-a-time) removal baseline, and a dependency-aware variant that
// only changes candidate *ordering*, never acceptance criteria -- per ADR-005, a
// candidate is never rejected solely because of an observed dependency link.
//
// The package is deliberately decoupled from Docker/HTTP/Postgres: callers provide
// an Executor, which lets the algorithms be unit-tested without the real stand
// (src/reducer/reducer_test.go) and reused by the controller against the real one.
//
// Guarantees shared by every algorithm (FR08, NFR06):
//   - the original failure is confirmed 3/3 on the full set before any shrinking;
//   - a candidate is accepted only after it reproduces the original failure and then
//     reproduces it 3/3 again on fresh executions; the last confirmed set is what is
//     reported, even if a later candidate is unstable or the budget runs out;
//   - every execution (screening, confirmation, empty-set probe) is charged against
//     one budget (runs and wall clock); cache hits are free and counted separately;
//   - the empty set is probed before the result is called minimal, so "minimal" means
//     1-minimal over valid candidates -- never a proven global minimum.
package reducer

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
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
// A non-nil error means an infrastructure problem (never a verdict about the bug).
type Executor interface {
	Execute(ctx context.Context, interventionIDs []string) (Outcome, error)
}

const (
	// DefaultMaxRuns and DefaultMaxDuration are the NFR06 limits: 100 executions
	// (confirmations included) or 10 minutes, whichever is reached first.
	DefaultMaxRuns     = 100
	DefaultMaxDuration = 10 * time.Minute

	// ConfirmationRuns is the number of additional executions an accepted candidate
	// must reproduce the original failure on (3/3).
	ConfirmationRuns = 3
)

type Budget struct {
	MaxRuns     int
	MaxDuration time.Duration
}

// Clamp enforces the NFR06 ceiling on a (scenario-supplied) budget.
func (b Budget) Clamp() Budget {
	if b.MaxRuns > DefaultMaxRuns {
		b.MaxRuns = DefaultMaxRuns
	}
	if b.MaxDuration > DefaultMaxDuration {
		b.MaxDuration = DefaultMaxDuration
	}
	return b
}

type StopReason string

const (
	StopMinimal        StopReason = "minimal"
	StopBudgetRuns     StopReason = "BUDGET_EXHAUSTED_RUNS"
	StopBudgetDuration StopReason = "BUDGET_EXHAUSTED_SECONDS"
	StopCancelled      StopReason = "CANCELLED"
	StopUnstable       StopReason = "UNSTABLE_ORIGINAL"
	StopInfraError     StopReason = "INFRA_ERROR"
	// StopOriginalNotReproduced: the first execution of the full set did not reproduce
	// the original failure, so there is nothing to reduce.
	StopOriginalNotReproduced StopReason = "ORIGINAL_NOT_REPRODUCED"
)

// IsBudget reports whether the reduction stopped on a NFR06 budget boundary.
func (r StopReason) IsBudget() bool { return r == StopBudgetRuns || r == StopBudgetDuration }

type Result struct {
	// Accepted is the last candidate confirmed 3/3 (the full set if nothing smaller
	// was confirmed). When Confirmed is false even the full set was not confirmed and
	// Accepted is only the input.
	Accepted   []string
	Confirmed  bool
	RunsUsed   int // every execution charged to the budget, confirmations included
	StopReason StopReason
	// Unstable is true if any candidate reproduced once but failed 3/3 confirmation.
	Unstable  bool
	Algorithm string

	// OneMinimal is true only when the search finished (StopMinimal): every
	// single-element removal of Accepted -- including removal of the last element,
	// i.e. the empty set -- was tried (or was an invalid candidate) and did not
	// reproduce. It is NOT a proof of global minimum cardinality.
	OneMinimal bool

	ConfirmationRuns int // executions spent on 3/3 confirmation (subset of RunsUsed)
	CacheHits        int // candidates answered from the result cache (not charged)
	SkippedInvalid   int // candidates rejected by Options.Valid without executing
	UnstableRejected int // candidates that reproduced once but failed confirmation
	OriginalOutcome  Outcome
	// Decisions lists, in order, every candidate that reproduced the original failure on
	// its screening run together with its confirmation verdict (the full original set
	// first). Candidates that did not reproduce, were invalid, or were answered from the
	// cache have no entry. The controller persists these as candidates.accepted /
	// candidates.confirmations.
	Decisions []Decision
	Error            string // set for StopInfraError / StopCancelled
	Duration         time.Duration
}

// Decision is the confirmation verdict for one candidate that reproduced on screening.
// Confirmations is how many of the ConfirmationRuns reruns reproduced the failure
// (ConfirmationRuns when Accepted; fewer when it fluctuated or the run stopped early).
type Decision struct {
	Candidate     []string
	Accepted      bool
	Confirmations int
}

// Options carries the pieces that are identical across algorithms except for Order.
type Options struct {
	// Order, if non-nil, reorders the active set before each split (dependency-aware
	// variant; ADR-005: ordering only, never an acceptance criterion).
	Order func([]string) []string
	// Valid, if non-nil, is the shared scenario-validity check applied identically to
	// every algorithm (explicit prerequisites, ADR-005). Invalid candidates are not
	// executed and are treated as non-reproducing.
	Valid func([]string) bool
}

type Algorithm string

const (
	AlgoDDMin           Algorithm = "ddmin"
	AlgoSequential      Algorithm = "sequential"
	AlgoDependencyAware Algorithm = "dependency_aware"
)

// Reduce is the original entry point, kept for backward compatibility.
func Reduce(ctx context.Context, exec Executor, all []string, algo Algorithm, budget Budget, order func([]string) []string) (*Result, error) {
	return ReduceWithOptions(ctx, exec, all, algo, budget, Options{Order: order})
}

// ReduceWithOptions validates the original failure (3/3) on the full set, then delegates to
// the chosen algorithm. The returned error is non-nil only for caller mistakes (unknown
// algorithm); infrastructure errors, cancellation and budget exhaustion are reported in
// Result.StopReason together with the best confirmed set.
func ReduceWithOptions(ctx context.Context, exec Executor, all []string, algo Algorithm, budget Budget, opts Options) (*Result, error) {
	switch algo {
	case AlgoDDMin, AlgoSequential, AlgoDependencyAware:
	default:
		return nil, fmt.Errorf("unknown algorithm %q", algo)
	}
	start := time.Now()
	s := &session{
		ctx: ctx, exec: exec, opts: opts,
		deadline:  start.Add(budget.MaxDuration),
		maxRuns:   budget.MaxRuns,
		cache:     map[string]bool{},
		confirmed: append([]string{}, all...),
	}
	if algo != AlgoDependencyAware {
		s.opts.Order = nil // ordering belongs to the dependency-aware variant only
	}
	res := s.run(all, algo)
	res.Algorithm = string(algo)
	res.Duration = time.Since(start)
	return res, nil
}

// halt is an internal early-exit signal carrying the stop reason.
type halt struct {
	reason StopReason
	err    error
}

type session struct {
	ctx      context.Context
	exec     Executor
	opts     Options
	deadline time.Time
	maxRuns  int

	runsUsed         int
	confirmationRuns int
	cacheHits        int
	skippedInvalid   int
	unstableRejected int
	// cache maps a candidate (as a sorted set) to whether it was ACCEPTED (confirmed
	// 3/3). Rejected/non-reproducing/invalid candidates map to false. Only fully
	// decided candidates are cached, never single executions inside a confirmation.
	cache map[string]bool

	decisions []Decision
	confirmed []string // last candidate confirmed 3/3
	isConfirm bool     // whether `confirmed` has actually been confirmed
}

func key(ids []string) string {
	c := append([]string{}, ids...)
	sort.Strings(c)
	return strings.Join(c, "\x00") + "#" + fmt.Sprint(len(c))
}

func (s *session) exhausted() StopReason {
	if s.runsUsed >= s.maxRuns {
		return StopBudgetRuns
	}
	if !time.Now().Before(s.deadline) {
		return StopBudgetDuration
	}
	return ""
}

// execute charges exactly one execution to the budget. It refuses to start a new
// execution after cancellation or once either budget boundary is reached.
func (s *session) execute(ids []string) (Outcome, *halt) {
	if err := s.ctx.Err(); err != nil {
		return "", &halt{StopCancelled, err}
	}
	if reason := s.exhausted(); reason != "" {
		return "", &halt{reason, nil}
	}
	s.runsUsed++
	runCtx, cancel := context.WithDeadline(s.ctx, s.deadline)
	defer cancel()
	out, err := s.exec.Execute(runCtx, append([]string{}, ids...))
	if err != nil {
		switch {
		case s.ctx.Err() != nil:
			return "", &halt{StopCancelled, s.ctx.Err()}
		case errors.Is(runCtx.Err(), context.DeadlineExceeded):
			// The wall-clock budget expired mid-execution: the aborted run is charged and
			// discarded, and it is a budget stop, not an infrastructure failure.
			return "", &halt{StopBudgetDuration, nil}
		default:
			return "", &halt{StopInfraError, err}
		}
	}
	return out, nil
}

func reproduces(o Outcome) bool { return o == OutcomeOriginalFailure }

// confirm re-runs a candidate ConfirmationRuns times and requires every run to
// reproduce the original failure (3/3). All runs are charged to the budget.
func (s *session) confirm(ids []string) (ok bool, reproduced int, h *halt) {
	for i := 0; i < ConfirmationRuns; i++ {
		before := s.runsUsed
		out, h := s.execute(ids)
		s.confirmationRuns += s.runsUsed - before
		if h != nil {
			return false, reproduced, h // budget/cancel/infra mid-confirmation: NOT confirmed
		}
		if !reproduces(out) {
			return false, reproduced, nil
		}
		reproduced++
	}
	return true, reproduced, nil
}

func (s *session) decide(ids []string, accepted bool, confirmations int) {
	s.decisions = append(s.decisions, Decision{Candidate: append([]string{}, ids...), Accepted: accepted, Confirmations: confirmations})
}

// attempt decides whether a candidate becomes the new accepted set: it must be valid,
// reproduce the original failure on a screening run, and then pass 3/3 confirmation.
func (s *session) attempt(ids []string) (bool, *halt) {
	k := key(ids)
	if v, ok := s.cache[k]; ok {
		s.cacheHits++
		return v, nil
	}
	if s.opts.Valid != nil && !s.opts.Valid(ids) {
		s.skippedInvalid++
		s.cache[k] = false
		return false, nil
	}
	out, h := s.execute(ids)
	if h != nil {
		return false, h
	}
	if !reproduces(out) {
		s.cache[k] = false
		return false, nil
	}
	ok, n, h := s.confirm(ids)
	s.decide(ids, ok, n)
	if h != nil {
		return false, h
	}
	s.cache[k] = ok
	if !ok {
		s.unstableRejected++ // reproduced once, fluctuated -> rejected and marked unstable
		return false, nil
	}
	s.confirmed = append([]string{}, ids...)
	s.isConfirm = true
	return true, nil
}

func (s *session) result(reason StopReason, h *halt) *Result {
	r := &Result{
		Accepted:         append([]string{}, s.confirmed...),
		Confirmed:        s.isConfirm,
		RunsUsed:         s.runsUsed,
		StopReason:       reason,
		Unstable:         s.unstableRejected > 0,
		OneMinimal:       reason == StopMinimal,
		ConfirmationRuns: s.confirmationRuns,
		CacheHits:        s.cacheHits,
		SkippedInvalid:   s.skippedInvalid,
		UnstableRejected: s.unstableRejected,
		Decisions:        s.decisions,
	}
	if h != nil && h.err != nil {
		r.Error = h.err.Error()
	}
	return r
}

func (s *session) run(all []string, algo Algorithm) *Result {
	out, h := s.execute(all)
	if h != nil {
		return s.result(h.reason, h)
	}
	if !reproduces(out) {
		r := s.result(StopOriginalNotReproduced, nil)
		r.OriginalOutcome = out
		return r
	}
	ok, n, h := s.confirm(all)
	s.decide(all, ok, n)
	if h != nil {
		return s.result(h.reason, h)
	}
	if !ok {
		r := s.result(StopUnstable, nil)
		r.Unstable = true
		return r
	}
	s.isConfirm = true
	s.cache[key(all)] = true

	var stop *halt
	if algo == AlgoSequential {
		stop = s.sequential(all)
	} else {
		stop = s.ddmin(all)
	}
	if stop != nil {
		return s.result(stop.reason, stop)
	}
	return s.result(StopMinimal, nil)
}
