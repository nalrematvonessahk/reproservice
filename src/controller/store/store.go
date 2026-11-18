// Package store is the Postgres-backed metadata store described in
// docs/07-architecture.md: scenario/version/run/event/candidate/assertion, with
// append-only events. It is deliberately separate from the test-app database that
// src/runner and src/oracle talk to (ADR-004).
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"reproservice/internal/schema"
)

var ErrNotFound = errors.New("store: not found")

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) CreateScenarioVersion(ctx context.Context, versionID string, sc *schema.Scenario) error {
	body, err := json.Marshal(sc)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO scenario_versions (version_id, scenario_id, body) VALUES ($1,$2,$3)`,
		versionID, sc.ID, body)
	return err
}

func (s *Store) GetScenarioVersion(ctx context.Context, versionID string) (*schema.Scenario, error) {
	var body []byte
	err := s.pool.QueryRow(ctx, `SELECT body FROM scenario_versions WHERE version_id=$1`, versionID).Scan(&body)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var sc schema.Scenario
	if err := json.Unmarshal(body, &sc); err != nil {
		return nil, err
	}
	return &sc, nil
}

// CreateRun inserts a new run, or returns the existing one if (scenario_id,
// idempotency_key) was already used -- "повторный POST не создаёт второй run"
// (docs/07-architecture.md draft API).
func (s *Store) CreateRun(ctx context.Context, run *schema.Run) (existed bool, err error) {
	activeIDs, _ := json.Marshal(run.ActiveIDs)
	_, err = s.pool.Exec(ctx,
		`INSERT INTO runs (run_id, scenario_id, version_id, idempotency_key, state, active_intervention_ids)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		run.RunID, run.ScenarioID, run.VersionID, run.IdempotencyKey, run.State, activeIDs)
	if err == nil {
		return false, nil
	}
	existing, getErr := s.GetRunByIdempotencyKey(ctx, run.ScenarioID, run.IdempotencyKey)
	if getErr != nil {
		return false, fmt.Errorf("store: create run: %w", err)
	}
	*run = *existing
	return true, nil
}

func (s *Store) GetRunByIdempotencyKey(ctx context.Context, scenarioID, key string) (*schema.Run, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT run_id, scenario_id, version_id, idempotency_key, state, coalesce(outcome,''), coalesce(stop_reason,''), active_intervention_ids, created_at, updated_at
		 FROM runs WHERE scenario_id=$1 AND idempotency_key=$2`, scenarioID, key)
	return scanRun(row)
}

func (s *Store) GetRun(ctx context.Context, runID string) (*schema.Run, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT run_id, scenario_id, version_id, idempotency_key, state, coalesce(outcome,''), coalesce(stop_reason,''), active_intervention_ids, created_at, updated_at
		 FROM runs WHERE run_id=$1`, runID)
	return scanRun(row)
}

func scanRun(row pgx.Row) (*schema.Run, error) {
	var r schema.Run
	var activeIDs []byte
	err := row.Scan(&r.RunID, &r.ScenarioID, &r.VersionID, &r.IdempotencyKey, &r.State, &r.Outcome, &r.StopReason, &activeIDs, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal(activeIDs, &r.ActiveIDs)
	return &r, nil
}

// UpdateState enforces the legal-transition check at the store boundary too, so a
// race between two callers cannot silently corrupt the state machine.
func (s *Store) UpdateState(ctx context.Context, runID string, to schema.RunState) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var from schema.RunState
	if err := tx.QueryRow(ctx, `SELECT state FROM runs WHERE run_id=$1 FOR UPDATE`, runID).Scan(&from); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if !schema.CanTransition(from, to) {
		return fmt.Errorf("store: illegal transition %s -> %s for run %s", from, to, runID)
	}
	if _, err := tx.Exec(ctx, `UPDATE runs SET state=$2, updated_at=now() WHERE run_id=$1`, runID, to); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) SetOutcome(ctx context.Context, runID string, outcome schema.Outcome, stopReason string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE runs SET outcome=$2, stop_reason=$3, state=$4, updated_at=now() WHERE run_id=$1`,
		runID, outcome, stopReason, schema.StateCompleted)
	return err
}

func (s *Store) SetInfraError(ctx context.Context, runID, reason string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE runs SET state=$2, stop_reason=$3, updated_at=now() WHERE run_id=$1`,
		runID, schema.StateInfraError, reason)
	return err
}

func (s *Store) InsertEvent(ctx context.Context, ev schema.Event) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO events (event_id, run_id, logical_operation, source, target, method, normalized_path,
			attempt, parent_event_id, monotonic_offset_ms, action_id, phase, result, status_code)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,nullif($9,''),$10,nullif($11,''),$12,$13,nullif($14,0))
		ON CONFLICT (event_id) DO NOTHING`,
		ev.EventID, ev.RunID, ev.LogicalOperation, ev.Source, ev.Target, ev.Method, ev.NormalizedPath,
		ev.Attempt, ev.ParentEventID, ev.MonotonicOffsetMs, ev.ActionID, ev.Phase, ev.Result, ev.StatusCode)
	return err
}

func (s *Store) ListEvents(ctx context.Context, runID string, after time.Time, limit int) ([]schema.Event, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT event_id, run_id, logical_operation, source, target, method, normalized_path, attempt,
			coalesce(parent_event_id,''), monotonic_offset_ms, coalesce(action_id,''), phase, result,
			coalesce(status_code,0), created_at
		FROM events WHERE run_id=$1 AND created_at > $2 ORDER BY created_at ASC LIMIT $3`, runID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []schema.Event
	for rows.Next() {
		var e schema.Event
		if err := rows.Scan(&e.EventID, &e.RunID, &e.LogicalOperation, &e.Source, &e.Target, &e.Method, &e.NormalizedPath,
			&e.Attempt, &e.ParentEventID, &e.MonotonicOffsetMs, &e.ActionID, &e.Phase, &e.Result, &e.StatusCode, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) InsertAssertion(ctx context.Context, runID string, a schema.Assertion) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO assertions (run_id, invariant_id, expected, actual, violated, infra_error)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (run_id) DO UPDATE SET invariant_id=$2, expected=$3, actual=$4, violated=$5, infra_error=$6`,
		runID, a.InvariantID, a.Expected, a.Actual, a.Violated, a.InfraError)
	return err
}

func (s *Store) InsertCandidate(ctx context.Context, candidateID, reductionID, runID string, interventionIDs []string, outcome schema.Outcome, accepted bool, confirmations int) error {
	ids, _ := json.Marshal(interventionIDs)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO candidates (candidate_id, reduction_id, run_id, intervention_ids, outcome, accepted, confirmations)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		candidateID, reductionID, runID, ids, outcome, accepted, confirmations)
	return err
}

// reductionsDDL is also present in deploy/sql/metadata_schema.sql (fresh volumes);
// EnsureSchema applies it at controller start so an existing metadata volume, whose
// init script never re-runs, gets the table too. Keep the two in sync.
const reductionsDDL = `
CREATE TABLE IF NOT EXISTS reductions (
    reduction_id TEXT PRIMARY KEY,
    run_id       TEXT NOT NULL REFERENCES runs(run_id),
    state        TEXT NOT NULL,
    stop_reason  TEXT,
    body         JSONB NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
)`

func (s *Store) EnsureSchema(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, reductionsDDL)
	return err
}

// SaveReduction upserts the reduction record (RUNNING at start, COMPLETED at the end).
func (s *Store) SaveReduction(ctx context.Context, r schema.Reduction) error {
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO reductions (reduction_id, run_id, state, stop_reason, body)
		VALUES ($1,$2,$3,nullif($4,''),$5)
		ON CONFLICT (reduction_id) DO UPDATE SET state=$3, stop_reason=nullif($4,''), body=$5, updated_at=now()`,
		r.ReductionID, r.RunID, r.State, r.StopReason, body)
	return err
}

func (s *Store) GetReduction(ctx context.Context, reductionID string) (*schema.Reduction, error) {
	var body []byte
	err := s.pool.QueryRow(ctx, `SELECT body FROM reductions WHERE reduction_id=$1`, reductionID).Scan(&body)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var r schema.Reduction
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// SetCandidateDecision records the reducer's verdict on the SCREENING row of a
// candidate set: accepted = confirmed 3/3, confirmations = confirmation reruns that
// reproduced the failure. The screening row is the first row of that exact set (same
// order) in the reduction; later rows of the same set are its confirmation executions
// and keep the defaults (false / 0). A set is screened at most once per reduction
// (result cache), so this addresses exactly one row.
func (s *Store) SetCandidateDecision(ctx context.Context, reductionID string, interventionIDs []string, accepted bool, confirmations int) error {
	if interventionIDs == nil {
		interventionIDs = []string{}
	}
	ids, _ := json.Marshal(interventionIDs)
	_, err := s.pool.Exec(ctx, `
		UPDATE candidates SET accepted=$3, confirmations=$4
		WHERE candidate_id = (
			SELECT candidate_id FROM candidates
			WHERE reduction_id=$1 AND intervention_ids=$2::jsonb
			ORDER BY created_at, length(candidate_id), candidate_id LIMIT 1)`,
		reductionID, ids, accepted, confirmations)
	return err
}
