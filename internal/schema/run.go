package schema

import "time"

// RunState is the lifecycle state machine from docs/07-architecture.md:
// VALIDATING -> RESETTING -> READY -> RUNNING -> ASSERTING -> COMPLETED,
// with CANCELLED/INFRA_ERROR reachable from any working state.
type RunState string

const (
	StateValidating RunState = "VALIDATING"
	StateResetting  RunState = "RESETTING"
	StateReady      RunState = "READY"
	StateRunning    RunState = "RUNNING"
	StateAsserting  RunState = "ASSERTING"
	StateCompleted  RunState = "COMPLETED"
	StateCancelled  RunState = "CANCELLED"
	StateInfraError RunState = "INFRA_ERROR"
)

// Outcome is only meaningful once State == COMPLETED.
type Outcome string

const (
	OutcomePass             Outcome = "PASS"
	OutcomeOriginalFailure  Outcome = "ORIGINAL_FAILURE"
	OutcomeOtherFailure     Outcome = "OTHER_FAILURE"
	OutcomeTimeout          Outcome = "TIMEOUT"
	OutcomeInapplicable     Outcome = "INAPPLICABLE"
)

// workingStates can transition to CANCELLED or INFRA_ERROR at any time.
var workingStates = map[RunState]bool{
	StateValidating: true,
	StateResetting:  true,
	StateReady:      true,
	StateRunning:    true,
	StateAsserting:  true,
}

// terminal states, once reached, accept no further transition.
var terminalStates = map[RunState]bool{
	StateCompleted:  true,
	StateCancelled:  true,
	StateInfraError: true,
}

// allowedForward are the normal, non-abort forward edges of the state machine.
var allowedForward = map[RunState]RunState{
	StateValidating: StateResetting,
	StateResetting:  StateReady,
	StateReady:      StateRunning,
	StateRunning:    StateAsserting,
	StateAsserting:  StateCompleted,
}

// CanTransition reports whether `from -> to` is a legal edge of the run state machine.
func CanTransition(from, to RunState) bool {
	if terminalStates[from] {
		return false
	}
	if to == StateCancelled || to == StateInfraError {
		return workingStates[from]
	}
	return allowedForward[from] == to
}

type EventPhase string

const (
	PhaseRequest  EventPhase = "request"
	PhaseResponse EventPhase = "response"
	PhaseFault    EventPhase = "fault"
)

type EventResult string

const (
	ResultForwarded EventResult = "forwarded"
	ResultFaulted   EventResult = "faulted"
	ResultDropped   EventResult = "dropped"
)

// Event is the append-only proxy log record, matched against intervention selectors.
// Sensitive headers are redacted before this struct is persisted (NFR02).
type Event struct {
	EventID           string      `json:"event_id"`
	RunID             string      `json:"run_id"`
	LogicalOperation  string      `json:"logical_operation"`
	Source            string      `json:"source"`
	Target            string      `json:"target"`
	Method            string      `json:"method"`
	NormalizedPath    string      `json:"normalized_path"`
	Attempt           int         `json:"attempt"`
	ParentEventID     string      `json:"parent_event_id,omitempty"`
	MonotonicOffsetMs int64       `json:"monotonic_offset_ms"`
	ActionID          string      `json:"action_id,omitempty"`
	Phase             EventPhase  `json:"phase"`
	Result            EventResult `json:"result"`
	StatusCode        int         `json:"status_code,omitempty"`
	CreatedAt         time.Time   `json:"created_at"`
}

func (e Event) Selector() Selector {
	return Selector{
		LogicalOperation: e.LogicalOperation,
		Source:           e.Source,
		Target:           e.Target,
		Method:           e.Method,
		NormalizedPath:   e.NormalizedPath,
		Attempt:          e.Attempt,
	}
}

// Assertion is the oracle's verdict for a run, carrying the specific invariant ID
// per FR06 -- an infra failure must never be reported as an invariant violation.
type Assertion struct {
	RunID       string  `json:"run_id"`
	InvariantID string  `json:"invariant_id"`
	Expected    string  `json:"expected"`
	Actual      string  `json:"actual"`
	Violated    bool    `json:"violated"`
	InfraError  bool    `json:"infra_error"`
	// Kind says HOW the invariant was violated ("duplicate" or "missing"); empty when
	// not violated. It is not persisted: the orchestrator uses it to tell the original
	// business failure (a duplicate charge) from an unrelated one (no charge at all).
	Kind ViolationKind `json:"kind,omitempty"`
}

type ViolationKind string

const (
	ViolationDuplicate ViolationKind = "duplicate"
	ViolationMissing   ViolationKind = "missing"
)

// Run is one execution of a scenario version against the stand.
type Run struct {
	RunID          string    `json:"run_id"`
	ScenarioID     string    `json:"scenario_id"`
	VersionID      string    `json:"version_id"`
	IdempotencyKey string    `json:"idempotency_key"`
	State          RunState  `json:"state"`
	Outcome        Outcome   `json:"outcome,omitempty"`
	StopReason     string    `json:"stop_reason,omitempty"`
	ActiveIDs      []string  `json:"active_intervention_ids"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// APIError is the error envelope required by docs/07-architecture.md's draft API:
// "code, message, field и correlation_id".
type APIError struct {
	Code          string `json:"code"`
	Message       string `json:"message"`
	Field         string `json:"field,omitempty"`
	CorrelationID string `json:"correlation_id"`
}

// Reduction is the persisted and API-visible result of one reduction (FR08, NFR06):
// the last candidate confirmed 3/3, the budget actually consumed, and why it stopped.
type Reduction struct {
	ReductionID      string   `json:"reduction_id"`
	RunID            string   `json:"run_id"`
	Algorithm        string   `json:"algorithm"`
	State            string   `json:"state"` // RUNNING | COMPLETED
	Accepted         []string `json:"accepted_intervention_ids"`
	Confirmed        bool     `json:"confirmed"`
	OneMinimal       bool     `json:"one_minimal"`
	Unstable         bool     `json:"unstable"`
	RunsUsed         int      `json:"runs_used"`
	MaxRuns          int      `json:"max_runs"`
	MaxSeconds       int      `json:"max_seconds"`
	ConfirmationRuns int      `json:"confirmation_runs"`
	CacheHits        int      `json:"cache_hits"`
	SkippedInvalid   int      `json:"skipped_invalid"`
	StopReason       string   `json:"stop_reason"`
	Error            string   `json:"error,omitempty"`
	DurationMs       int64    `json:"duration_ms"`
}
