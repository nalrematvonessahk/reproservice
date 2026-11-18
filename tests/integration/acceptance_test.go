//go:build integration

// DEMO-52 acceptance tests for FR02, FR06, FR08, FR09, NFR06 against the real stand.
// Run with the stand up: go test -tags=integration ./tests/integration/...
package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type iv struct {
	ID      string
	Target  string
	Path    string
	Attempt int
	Action  map[string]any
}

// scenarioJSON builds an executable scenario on the demo stand.
func scenarioJSON(id string, ivs ...iv) []byte {
	var list []map[string]any
	for _, v := range ivs {
		list = append(list, map[string]any{
			"id": v.ID,
			"selector": map[string]any{
				"logical_operation": id, "source": "orders", "target": v.Target,
				"method": "POST", "normalized_path": v.Path, "attempt": v.Attempt,
			},
			"action": v.Action, "depends_on": []string{},
		})
	}
	b, _ := json.Marshal(map[string]any{
		"schema_version": 1, "id": id,
		"environment":   map[string]any{"compose_profile": "orders-demo", "image_digests": map[string]string{}, "seed_id": "seed-001", "seed_hash": "TO_BE_COMPUTED"},
		"invariant_id":  "payment_exactly_once",
		"budget":        map[string]any{"runs": 100, "seconds": 600},
		"interventions": list,
	})
	return b
}

func drop(id string) iv {
	return iv{ID: id, Target: "payments", Path: "/charge", Attempt: 1, Action: map[string]any{"type": "drop_response_after_success"}}
}
func slowDecoy(id string, ms int) iv {
	return iv{ID: id, Target: "inventory", Path: "/reserve", Attempt: 1, Action: map[string]any{"type": "delay", "delay_ms": ms}}
}

func versionOf(t *testing.T, body []byte) string {
	t.Helper()
	status, out := postJSON(t, "/api/scenarios", body)
	if status != 200 {
		t.Fatalf("POST /api/scenarios: %d %v", status, out)
	}
	return out["version_id"].(string)
}

func runAndWait(t *testing.T, versionID string, active []string) map[string]any {
	t.Helper()
	req := map[string]any{"version_id": versionID, "idempotency_key": fmt.Sprintf("acc-%d", time.Now().UnixNano())}
	if active != nil {
		req["active_intervention_ids"] = active
	}
	b, _ := json.Marshal(req)
	status, out := postJSON(t, "/api/runs", b)
	if status != 202 {
		t.Fatalf("POST /api/runs: %d %v", status, out)
	}
	return waitCompleted(t, out["run_id"].(string))
}

func reduce(t *testing.T, runID, algo string) map[string]any {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"algorithm": algo})
	status, out := postJSON(t, "/api/runs/"+runID+"/reduce", b)
	if status != 200 {
		t.Fatalf("reduce(%s): %d %v", algo, status, out)
	}
	return out
}

func strs(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

// FR06: the oracle distinguishes the original failure (double payment) from other
// failures, PASS and INAPPLICABLE; none of them is mistaken for another.
func TestOracle_ClassifiesOutcomes(t *testing.T) {
	t.Run("double payment -> ORIGINAL_FAILURE with expected/actual", func(t *testing.T) {
		run := runAndWait(t, versionOf(t, scenarioJSON("acc-orig", drop("fault-1"))), nil)
		if run["outcome"] != "ORIGINAL_FAILURE" {
			t.Fatalf("got %v", run)
		}
		if reason, _ := run["stop_reason"].(string); !strings.Contains(reason, "expected 1, actual 2") {
			t.Fatalf("detail should carry expected/actual, got %q", run["stop_reason"])
		}
	})
	t.Run("no payment ever made -> OTHER_FAILURE, not the original failure", func(t *testing.T) {
		e := func(id string, attempt int) iv {
			return iv{ID: id, Target: "payments", Path: "/charge", Attempt: attempt, Action: map[string]any{"type": "http_error", "status_code": 503}}
		}
		run := runAndWait(t, versionOf(t, scenarioJSON("acc-other", e("e1", 1), e("e2", 2))), nil)
		if run["outcome"] != "OTHER_FAILURE" {
			t.Fatalf("expected OTHER_FAILURE (0 payments), got %v", run)
		}
	})
	t.Run("no fault -> PASS", func(t *testing.T) {
		run := runAndWait(t, versionOf(t, scenarioJSON("acc-pass", drop("fault-1"))), []string{})
		if run["outcome"] != "PASS" {
			t.Fatalf("got %v", run)
		}
	})
	t.Run("selector that never matches -> INAPPLICABLE, not PASS", func(t *testing.T) {
		never := drop("fault-1")
		never.Attempt = 7
		run := runAndWait(t, versionOf(t, scenarioJSON("acc-inapp", never)), nil)
		if run["outcome"] != "INAPPLICABLE" {
			t.Fatalf("expected INAPPLICABLE, got %v", run)
		}
	})
}

// FR08/NFR06: all three algorithms reduce both required scenarios to the same
// confirmed set, report the budget they spent, and the result is repeatable.
func TestReduction_AllAlgorithms_ConfirmedBudgetedRepeatable(t *testing.T) {
	cases := []struct {
		name, file string
	}{
		{"scenario.example", "../../config/scenario.example.json"},
		{"multi-fault-demo", "../../config/scenarios/multi-fault-demo.json"},
	}
	for _, c := range cases {
		for _, algo := range []string{"ddmin", "sequential", "dependency_aware"} {
			t.Run(c.name+"/"+algo, func(t *testing.T) {
				vid := createVersion(t, c.file)
				run := runAndWait(t, vid, nil)
				if run["outcome"] != "ORIGINAL_FAILURE" {
					t.Fatalf("original must reproduce, got %v", run["outcome"])
				}
				runID := run["run_id"].(string)

				red := reduce(t, runID, algo)
				if got := strs(red["accepted_intervention_ids"]); len(got) != 1 || got[0] != "fault-1" {
					t.Fatalf("expected [fault-1], got %v (%v)", got, red)
				}
				if red["stop_reason"] != "minimal" || red["confirmed"] != true || red["one_minimal"] != true || red["unstable"] != false {
					t.Fatalf("expected confirmed 1-minimal result, got %v", red)
				}
				used, max := red["runs_used"].(float64), red["max_runs"].(float64)
				if used > max || max != 100 || red["max_seconds"].(float64) != 600 {
					t.Fatalf("budget accounting wrong: used=%v max=%v secs=%v", used, max, red["max_seconds"])
				}
				// original 1 + 3 confirmations + empty-set probe is the floor (multi-fault: + candidate + 3).
				if used < 5 || red["confirmation_runs"].(float64) < 3 {
					t.Fatalf("confirmations must be charged: %v", red)
				}

				// Persisted and retrievable.
				status, stored := getJSON(t, "/api/reductions/"+red["reduction_id"].(string))
				if status != 200 || stored["stop_reason"] != "minimal" || stored["runs_used"] != red["runs_used"] {
					t.Fatalf("stored reduction mismatch: %d %v", status, stored)
				}

				// Repeatable: reducing the SAME run again must work (candidate IDs are unique).
				red2 := reduce(t, runID, algo)
				if got := strs(red2["accepted_intervention_ids"]); len(got) != 1 || got[0] != "fault-1" || red2["reduction_id"] == red["reduction_id"] {
					t.Fatalf("second reduction of the same run: %v", red2)
				}

				// The reduced scenario itself reproduces the original failure 3/3.
				for i := 1; i <= 3; i++ {
					r := runAndWait(t, vid, strs(red["accepted_intervention_ids"]))
					if r["outcome"] != "ORIGINAL_FAILURE" {
						t.Fatalf("reduced scenario reproduction %d/3 failed: %v", i, r)
					}
				}
			})
		}
	}
}

// FR09: cancel stops a run in flight with CANCELLED (never PASS), and stops a reduction
// from starting new candidates, reporting stop_reason CANCELLED; the stand is released.
func TestCancel_RunAndReduction(t *testing.T) {
	// A 2.5 s decoy makes every candidate slow enough to cancel deterministically.
	slow := versionOf(t, scenarioJSON("acc-slow", drop("fault-1"), slowDecoy("fault-2-decoy", 2500)))

	t.Run("running run -> CANCELLED", func(t *testing.T) {
		b, _ := json.Marshal(map[string]any{"version_id": slow, "idempotency_key": fmt.Sprintf("acc-cr-%d", time.Now().UnixNano())})
		_, out := postJSON(t, "/api/runs", b)
		runID := out["run_id"].(string)
		time.Sleep(800 * time.Millisecond)
		status, c := postJSON(t, "/api/runs/"+runID+"/cancel", nil)
		if status != 200 || c["status"] != "cancel_requested" {
			t.Fatalf("cancel: %d %v", status, c)
		}
		run := waitCompleted(t, runID)
		if run["state"] != "CANCELLED" || run["outcome"] == "PASS" || run["outcome"] == "ORIGINAL_FAILURE" {
			t.Fatalf("expected CANCELLED without a verdict, got %v", run)
		}
	})

	t.Run("cancel on finished run is a reported no-op", func(t *testing.T) {
		run := runAndWait(t, createVersion(t, "../../config/scenario.example.json"), nil)
		status, c := postJSON(t, "/api/runs/"+run["run_id"].(string)+"/cancel", nil)
		if status != 200 || c["status"] != "not_running" || c["state"] != "COMPLETED" {
			t.Fatalf("got %d %v", status, c)
		}
		_, after := getJSON(t, "/api/runs/"+run["run_id"].(string))
		if after["outcome"] != "ORIGINAL_FAILURE" {
			t.Fatalf("a finished run must not be rewritten: %v", after)
		}
	})

	t.Run("reduction -> stop_reason CANCELLED, best confirmed kept, stand freed", func(t *testing.T) {
		parent := runAndWait(t, slow, nil)
		if parent["outcome"] != "ORIGINAL_FAILURE" {
			t.Fatalf("parent: %v", parent)
		}
		runID := parent["run_id"].(string)
		done := make(chan map[string]any, 1)
		go func() {
			b, _ := json.Marshal(map[string]string{"algorithm": "ddmin"})
			_, out := postJSON(t, "/api/runs/"+runID+"/reduce", b)
			done <- out
		}()
		time.Sleep(1500 * time.Millisecond) // mid-candidate: every candidate takes >= 2.5 s
		status, c := postJSON(t, "/api/runs/"+runID+"/cancel", nil)
		if status != 200 || c["status"] != "cancel_requested" {
			t.Fatalf("cancel during reduce: %d %v", status, c)
		}
		select {
		case red := <-done:
			if red["stop_reason"] != "CANCELLED" || red["state"] != "COMPLETED" {
				t.Fatalf("expected CANCELLED reduction, got %v", red)
			}
			if got := strs(red["accepted_intervention_ids"]); len(got) != 2 {
				t.Fatalf("no candidate was confirmed yet; the full set must be kept, got %v", got)
			}
			if red["confirmed"] != false {
				t.Fatalf("nothing confirmed yet, confirmed must be false: %v", red)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("reduction did not stop after cancel")
		}
		_, p := getJSON(t, "/api/runs/"+runID)
		if p["outcome"] != "ORIGINAL_FAILURE" {
			t.Fatalf("cancel must not rewrite the parent run: %v", p)
		}
		// Stand is free again.
		time.Sleep(300 * time.Millisecond)
		if r := runAndWait(t, createVersion(t, "../../config/scenario.example.json"), nil); r["outcome"] != "ORIGINAL_FAILURE" {
			t.Fatalf("stand not usable after cancel: %v", r)
		}
	})
}

// FR02: every run starts from the seeded state; rows written by the previous run are
// gone after the next reset. Needs docker + the compose stand (skipped otherwise).
func TestReset_ClearsPreviousRunState(t *testing.T) {
	psql := func(q string) (string, error) {
		cmd := exec.Command("docker", "compose", "-f", "../../deploy/docker-compose.yml", "exec", "-T", "postgres-testapp",
			"psql", "-U", "reproservice", "-d", "testapp", "-tAc", q)
		var out bytes.Buffer
		cmd.Stdout = &out
		err := cmd.Run()
		return strings.TrimSpace(out.String()), err
	}
	if _, err := psql("select 1"); err != nil {
		t.Skipf("docker compose exec unavailable: %v", err)
	}
	vid := createVersion(t, "../../config/scenario.example.json")
	first := runAndWait(t, vid, nil)
	id1 := first["run_id"].(string)
	if n, _ := psql(fmt.Sprintf("select count(*) from payments where order_id='%s'", id1)); n != "2" {
		t.Fatalf("expected the double payment to be present after run 1, got %q", n)
	}
	runAndWait(t, vid, []string{}) // next run: reset happens first
	if n, _ := psql(fmt.Sprintf("select count(*) from payments where order_id='%s'", id1)); n != "0" {
		t.Fatalf("reset did not clear run 1's payments, got %q", n)
	}
	if n, _ := psql("select (select count(*) from orders)||','||(select count(*) from inventory_reservations)"); n != "1,1" {
		t.Fatalf("only the latest run's rows may remain (1 order, 1 reservation), got %q", n)
	}
}

// ADR-005 / FR08: an unknown algorithm is a client error, not a silent default.
func TestReduce_UnknownAlgorithmRejected(t *testing.T) {
	run := runAndWait(t, createVersion(t, "../../config/scenario.example.json"), nil)
	status, out := postJSON(t, "/api/runs/"+run["run_id"].(string)+"/reduce", []byte(`{"algorithm":"magic"}`))
	if status != 422 {
		t.Fatalf("expected 422, got %d %v", status, out)
	}
}

// A client typo in active_intervention_ids is a validation error, not a scenario verdict.
func TestCreateRun_UnknownInterventionIDRejected(t *testing.T) {
	vid := createVersion(t, "../../config/scenario.example.json")
	b, _ := json.Marshal(map[string]any{"version_id": vid, "idempotency_key": fmt.Sprintf("acc-unk-%d", time.Now().UnixNano()),
		"active_intervention_ids": []string{"fault-1", "fault-l"}})
	status, out := postJSON(t, "/api/runs", b)
	if status != 422 || out["code"] != "UNKNOWN_INTERVENTION" {
		t.Fatalf("expected 422 UNKNOWN_INTERVENTION, got %d %v", status, out)
	}
}

// Two interventions sharing one selector are rejected at import (ambiguous match).
func TestInvalidScenario_DuplicateSelectorRejected(t *testing.T) {
	status, _ := postJSON(t, "/api/scenarios", scenarioJSON("acc-dupsel", drop("fault-1"), drop("fault-2")))
	if status != 422 {
		t.Fatalf("expected 422 for duplicate selectors, got %d", status)
	}
}
