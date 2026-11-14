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

	"github.com/jackc/pgx/v5/pgxpool"
)

type Runner struct {
	pool        *pgxpool.Pool
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
		seedSQL:    string(b),
		seedHash:   hex.EncodeToString(sum[:]),
		healthURLs: healthURLs,
		httpClient: &http.Client{Timeout: 3 * time.Second},
	}, nil
}

// SeedHash is compared against the scenario's recorded Environment.SeedHash per
// ADR-002: a replay against a mismatched seed must block, not silently proceed.
func (r *Runner) SeedHash() string { return r.seedHash }

// Reset truncates the run's own test-app tables and reloads the fixed seed. Only the
// tables this platform owns are touched -- no other containers or volumes.
func (r *Runner) Reset(ctx context.Context) error {
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
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("runner: commit reset tx: %w", err)
	}
	return r.verifyHealth(ctx)
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
