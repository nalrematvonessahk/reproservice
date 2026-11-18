// Package runner implements the "Runner/reset" component from docs/07-architecture.md:
// restoring the test-app database to its seed state and verifying health before each
// candidate run. It intentionally does not accept arbitrary SQL or shell commands from
// callers (ADR-004) -- the only inputs are a fixed seed file shipped in this repo and a
// fixed list of service health URLs.
package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Runner struct {
	pool        *pgxpool.Pool
	seedPath    string
	seedSQL     string
	seedHash    string
	healthURLs  []string
	httpClient  *http.Client
}

func New(pool *pgxpool.Pool, seedSQLPath string, healthURLs []string) (*Runner, error) {
	b, err := os.ReadFile(seedSQLPath)
	if err != nil {
		return nil, fmt.Errorf("runner: read seed file: %w", err)
	}
	sum := sha256.Sum256(b)
	return &Runner{
		pool:       pool,
		seedPath:   seedSQLPath,
		seedSQL:    string(b),
		seedHash:   hex.EncodeToString(sum[:]),
		healthURLs: healthURLs,
		httpClient: &http.Client{Timeout: 3 * time.Second},
	}, nil
}

// SeedHash is compared against the scenario's recorded Environment.SeedHash per
// ADR-002: a replay against a mismatched seed must block, not silently proceed.
func (r *Runner) SeedHash() string { return r.seedHash }

// ownedTables are the only tables Reset touches and verifies.
var ownedTables = []string{"orders", "payments", "inventory_reservations"}

// Reset truncates the run's own test-app tables and reloads the fixed seed. Only the
// tables this platform owns are touched -- no other containers or volumes. Any error
// (changed seed file, SQL failure, post-reset mismatch, failed health check) is
// returned so the caller does not start the run (FR02) and reports an infrastructure
// error, never an application failure.
func (r *Runner) Reset(ctx context.Context) error {
	// ADR-002 / docs/07-architecture.md: the seed hash is re-verified before every
	// reset, so an edited seed file blocks the run instead of silently changing the
	// starting state.
	cur, err := os.ReadFile(r.seedPath)
	if err != nil {
		return fmt.Errorf("runner: re-read seed file: %w", err)
	}
	if sum := sha256.Sum256(cur); hex.EncodeToString(sum[:]) != r.seedHash {
		return fmt.Errorf("runner: seed file changed since startup (hash mismatch); refusing to run")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("runner: begin reset tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `TRUNCATE orders, payments, inventory_reservations`); err != nil {
		return fmt.Errorf("runner: truncate: %w", err)
	}
	if r.seedSQL != "" {
		if _, err := tx.Exec(ctx, r.seedSQL); err != nil {
			return fmt.Errorf("runner: apply seed: %w", err)
		}
	}
	want, err := countTables(ctx, tx)
	if err != nil {
		return fmt.Errorf("runner: count seeded rows: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("runner: commit reset tx: %w", err)
	}

	// Post-commit verification: the committed state must still be exactly the seeded
	// state. A difference means something kept writing during reset (e.g. a leftover
	// request from the previous candidate), which would contaminate this candidate.
	got, err := countTables(ctx, r.pool)
	if err != nil {
		return fmt.Errorf("runner: verify reset: %w", err)
	}
	for _, t := range ownedTables {
		if got[t] != want[t] {
			return fmt.Errorf("runner: reset verification failed: table %s has %d rows, seed state has %d", t, got[t], want[t])
		}
	}
	return r.verifyHealth(ctx)
}

type queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func countTables(ctx context.Context, q queryer) (map[string]int, error) {
	out := make(map[string]int, len(ownedTables))
	for _, t := range ownedTables {
		var n int
		if err := q.QueryRow(ctx, "SELECT count(*) FROM "+t).Scan(&n); err != nil { // t is from the fixed list above
			return nil, err
		}
		out[t] = n
	}
	return out, nil
}

// verifyHealth is the post-reset check required before a run may leave RESETTING for
// READY (docs/07-architecture.md: "После reset сверяются хеш seed и health checks").
func (r *Runner) verifyHealth(ctx context.Context) error {
	for _, url := range r.healthURLs {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return fmt.Errorf("runner: build health request for %s: %w", url, err)
		}
		resp, err := r.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("runner: health check failed for %s: %w", url, err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("runner: health check for %s returned %d", url, resp.StatusCode)
		}
	}
	return nil
}
