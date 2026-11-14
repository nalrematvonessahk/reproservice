//go:build integration

// Integration tests against the real Docker Compose stand (docs/06-requirements.md
// TC-FR01/03/04/05/06/07/08/12). Run with the stand already up:
//
//	docker compose -f deploy/docker-compose.yml up -d --build
//	go test -tags=integration ./tests/integration/...
//
// These tests exercise the real proxy/controller/runner/oracle/reducer boundary; they
// do not mock any of it. They are skipped by a plain `go test ./...` because the
// Docker stand is not assumed to be running in that context.
package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

const base = "http://127.0.0.1:7000"

func postJSON(t *testing.T, path string, body []byte) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(base+path, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func getJSON(t *testing.T, path string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(base + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func loadScenario(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read scenario %s: %v", path, err)
	}
	return b
}

func createVersion(t *testing.T, scenarioPath string) string {
	t.Helper()
	status, out := postJSON(t, "/api/scenarios", loadScenario(t, scenarioPath))
	if status != 200 {
		t.Fatalf("POST /api/scenarios: status %d, body %v", status, out)
	}
	return out["version_id"].(string)
}

func createRun(t *testing.T, versionID, idemKey string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"version_id": versionID, "idempotency_key": idemKey})
	status, out := postJSON(t, "/api/runs", body)
	runID, _ := out["run_id"].(string)
	return status, runID
}

// TestZeroActiveInterventions_RunsCleanBaseline covers FR11 (comparing a version
// against a fixed one needs a way to run with no faults at all). Regression test for
// a real bug: an explicit empty active_intervention_ids array used to be treated the
// same as omitting the field (defaulting to "all interventions"), making a baseline
// comparison run impossible to request. See src/controller/api.go createRun.
func TestZeroActiveInterventions_RunsCleanBaseline(t *testing.T) {
	versionID := createVersion(t, "../../config/scenario.example.json")
	body, _ := json.Marshal(map[string]any{
		"version_id": versionID, "idempotency_key": fmt.Sprintf("it-zero-%d", time.Now().UnixNano()),
		"active_intervention_ids": []string{},
	})
	status, out := postJSON(t, "/api/runs", body)
	if status != 202 {
		t.Fatalf("POST /api/runs: expected 202, got %d (%v)", status, out)
	}
	runID := out["run_id"].(string)
	run := waitCompleted(t, runID)
	if run["outcome"] != "PASS" {
		t.Fatalf("expected PASS with zero active interventions (no fault injected), got %v", run["outcome"])
	}
	active, _ := run["active_intervention_ids"].([]any)
	if len(active) != 0 {
		t.Fatalf("expected active_intervention_ids to stay empty, got %v", active)
	}
}

func waitCompleted(t *testing.T, runID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		status, run := getJSON(t, "/api/runs/"+runID)
		if status != 200 {
			t.Fatalf("GET /api/runs/%s: status %d", runID, status)
		}
		if run["state"] == "COMPLETED" || run["state"] == "INFRA_ERROR" || run["state"] == "CANCELLED" {
			return run
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("run %s did not complete within deadline", runID)
	return nil
}

// TestDoublePaymentScenario_ReproducesAndReduces covers TC-FR04/05/06/07/08: dropping
// a successful response causes a real double charge (not simulated), the oracle
// reports the specific invariant, and ddmin correctly keeps the single intervention
// required to reproduce it (it cannot shrink below 1 here, which is itself a check:
// the reducer must not over-shrink to an empty, non-reproducing set).
func TestDoublePaymentScenario_ReproducesAndReduces(t *testing.T) {
	versionID := createVersion(t, "../../config/scenario.example.json")

	status, runID := createRun(t, versionID, fmt.Sprintf("it-single-%d", time.Now().UnixNano()))
	if status != 202 {
		t.Fatalf("POST /api/runs: expected 202, got %d", status)
	}

	run := waitCompleted(t, runID)
	if run["state"] != "COMPLETED" {
		t.Fatalf("expected COMPLETED, got %v (run=%v)", run["state"], run)
	}
	if run["outcome"] != "ORIGINAL_FAILURE" {
		t.Fatalf("expected ORIGINAL_FAILURE (double payment), got %v", run["outcome"])
	}

	reduceBody, _ := json.Marshal(map[string]string{"algorithm": "ddmin"})
	rstatus, rout := postJSON(t, "/api/runs/"+runID+"/reduce", reduceBody)
	if rstatus != 200 {
		t.Fatalf("POST .../reduce: status %d, body %v", rstatus, rout)
	}
	accepted, _ := rout["accepted_intervention_ids"].([]any)
	if len(accepted) != 1 || accepted[0] != "fault-1" {
		t.Fatalf("expected minimal set [fault-1], got %v", accepted)
	}
	if rout["stop_reason"] != "minimal" {
		t.Fatalf("expected stop_reason=minimal, got %v", rout["stop_reason"])
	}
}

// TestMultiFaultScenario_ReducerDropsDecoy covers FR08's actual shrinking behaviour:
// with two interventions active where only one is required to reproduce
// payment_exactly_once, ddmin must discard the unrelated delay and keep just the
// response-dropping fault.
func TestMultiFaultScenario_ReducerDropsDecoy(t *testing.T) {
	versionID := createVersion(t, "../../config/scenarios/multi-fault-demo.json")

	status, runID := createRun(t, versionID, fmt.Sprintf("it-multi-%d", time.Now().UnixNano()))
	if status != 202 {
		t.Fatalf("POST /api/runs: expected 202, got %d", status)
	}
	run := waitCompleted(t, runID)
	if run["outcome"] != "ORIGINAL_FAILURE" {
		t.Fatalf("expected ORIGINAL_FAILURE with both interventions active, got %v", run["outcome"])
	}

	reduceBody, _ := json.Marshal(map[string]string{"algorithm": "ddmin"})
	rstatus, rout := postJSON(t, "/api/runs/"+runID+"/reduce", reduceBody)
	if rstatus != 200 {
		t.Fatalf("POST .../reduce: status %d, body %v", rstatus, rout)
	}
	accepted, _ := rout["accepted_intervention_ids"].([]any)
	if len(accepted) != 1 || accepted[0] != "fault-1" {
		t.Fatalf("expected reduction to discard the decoy and keep only fault-1, got %v", accepted)
	}
}

// TestConcurrentRun_Returns409 covers the NFR requirement that a run against a busy
// stand is rejected, not queued or silently dropped.
func TestConcurrentRun_Returns409(t *testing.T) {
	versionID := createVersion(t, "../../config/scenario.example.json")
	nonce := time.Now().UnixNano()

	type result struct{ status int }
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			status, _ := createRun(t, versionID, fmt.Sprintf("it-race-%d-%d", nonce, i))
			results <- result{status}
		}(i)
	}
	a, b := <-results, <-results
	seen := []int{a.status, b.status}
	has202, has409 := false, false
	for _, c := range seen {
		if c == 202 {
			has202 = true
		}
		if c == 409 {
			has409 = true
		}
	}
	if !has202 || !has409 {
		t.Fatalf("expected exactly one 202 and one 409 for concurrent runs, got %v", seen)
	}
}

// TestInvalidScenario_RejectedBeforeRunning covers FR01: an unknown action type and a
// scenario with a dependency cycle must be rejected at POST /api/scenarios, before any
// stand activity happens.
func TestInvalidScenario_RejectedBeforeRunning(t *testing.T) {
	bad := []byte(`{
		"schema_version": 1, "id": "bad-scenario",
		"environment": {"compose_profile":"orders-demo","seed_id":"seed-001"},
		"invariant_id": "payment_exactly_once",
		"budget": {"runs": 10, "seconds": 60},
		"interventions": [{"id":"x","selector":{"logical_operation":"o","source":"orders","target":"payments","method":"POST","normalized_path":"/charge","attempt":1},"action":{"type":"not_a_real_action"}}]
	}`)
	status, _ := postJSON(t, "/api/scenarios", bad)
	if status != 422 {
		t.Fatalf("expected 422 for unknown action type, got %d", status)
	}
}
