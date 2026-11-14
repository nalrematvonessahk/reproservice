package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"reproservice/internal/httpx"
	"reproservice/internal/schema"
	"reproservice/src/controller/store"
	"reproservice/src/reducer"
)

type API struct {
	store *store.Store
	orch  *Orchestrator
}

func NewAPI(st *store.Store, orch *Orchestrator) *API { return &API{store: st, orch: orch} }

func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/scenarios", a.createScenario)
	mux.HandleFunc("POST /api/runs", a.createRun)
	mux.HandleFunc("GET /api/runs/{id}", a.getRun)
	mux.HandleFunc("GET /api/runs/{id}/events", a.listEvents)
	mux.HandleFunc("POST /api/runs/{id}/cancel", a.cancelRun)
	mux.HandleFunc("POST /api/runs/{id}/reduce", a.reduceRun)
	mux.HandleFunc("GET /api/runs/{id}/export", a.exportRun)
	mux.HandleFunc("POST /internal/runs/{run_id}/events", a.ingestEvent)
	return mux
}

func corrID() string { return newID("corr") }

func (a *API) createScenario(w http.ResponseWriter, r *http.Request) {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteError(w, 400, "INVALID_BODY", err.Error(), corrID())
		return
	}
	sc, err := schema.ParseScenario(b)
	if err != nil {
		httpx.WriteError(w, 422, "INVALID_SCENARIO", err.Error(), corrID())
		return
	}
	versionID := newID("ver")
	if err := a.store.CreateScenarioVersion(r.Context(), versionID, sc); err != nil {
		httpx.WriteError(w, 500, "DB_ERROR", err.Error(), corrID())
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"schema_version": schema.SchemaVersion, "version_id": versionID, "scenario_id": sc.ID})
}

type createRunRequest struct {
	VersionID              string   `json:"version_id"`
	IdempotencyKey         string   `json:"idempotency_key"`
	ActiveInterventionIDs  []string `json:"active_intervention_ids,omitempty"`
}

func (a *API) createRun(w http.ResponseWriter, r *http.Request) {
	var req createRunRequest
	if err := httpx.DecodeJSON(r, &req); err != nil || req.VersionID == "" || req.IdempotencyKey == "" {
		httpx.WriteError(w, 400, "INVALID_BODY", "version_id and idempotency_key are required", corrID())
		return
	}
	sc, err := a.store.GetScenarioVersion(r.Context(), req.VersionID)
	if err != nil {
		httpx.WriteError(w, 404, "VERSION_NOT_FOUND", err.Error(), corrID())
		return
	}

	// nil (field omitted) means "use every intervention in the version"; an explicit
	// [] means "run with zero active interventions" -- e.g. to execute the baseline/
	// fixed-version comparison in FR11, which needs a way to request no faults at all.
	active := req.ActiveInterventionIDs
	if active == nil {
		for _, iv := range sc.Interventions {
			active = append(active, iv.ID)
		}
	}

	if !a.orch.TryAcquire() {
		httpx.WriteError(w, 409, "STAND_BUSY", "another run is currently using the stand", corrID())
		return
	}

	run := &schema.Run{
		RunID: newID("run"), ScenarioID: sc.ID, VersionID: req.VersionID,
		IdempotencyKey: req.IdempotencyKey, State: schema.StateValidating, ActiveIDs: active,
	}
	existed, err := a.store.CreateRun(r.Context(), run)
	if err != nil {
		a.orch.Release()
		httpx.WriteError(w, 500, "DB_ERROR", err.Error(), corrID())
		return
	}
	if existed {
		a.orch.Release() // idempotent replay: no new execution will claim the stand
		httpx.WriteJSON(w, 200, run)
		return
	}

	go a.orch.ExecuteRun(run, sc)
	httpx.WriteJSON(w, 202, map[string]string{"run_id": run.RunID})
}

func (a *API) getRun(w http.ResponseWriter, r *http.Request) {
	run, err := a.store.GetRun(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, 404, "RUN_NOT_FOUND", err.Error(), corrID())
		return
	}
	httpx.WriteJSON(w, 200, run)
}

func (a *API) listEvents(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	after := time.Time{}
	if c := r.URL.Query().Get("cursor"); c != "" {
		if ms, err := strconv.ParseInt(c, 10, 64); err == nil {
			after = time.UnixMilli(ms)
		}
	}
	events, err := a.store.ListEvents(r.Context(), runID, after, 500)
	if err != nil {
		httpx.WriteError(w, 500, "DB_ERROR", err.Error(), corrID())
		return
	}
	nextCursor := ""
	if len(events) > 0 {
		nextCursor = strconv.FormatInt(events[len(events)-1].CreatedAt.UnixMilli(), 10)
	}
	httpx.WriteJSON(w, 200, map[string]any{"events": events, "cursor": nextCursor})
}

func (a *API) cancelRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	if _, err := a.store.GetRun(r.Context(), runID); err != nil {
		httpx.WriteError(w, 404, "RUN_NOT_FOUND", err.Error(), corrID())
		return
	}
	a.orch.Cancel(runID)
	httpx.WriteJSON(w, 200, map[string]string{"status": "cancel_requested"})
}

type reduceRequest struct {
	Algorithm string `json:"algorithm"`
}

func (a *API) reduceRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	run, err := a.store.GetRun(r.Context(), runID)
	if err != nil {
		httpx.WriteError(w, 404, "RUN_NOT_FOUND", err.Error(), corrID())
		return
	}
	sc, err := a.store.GetScenarioVersion(r.Context(), run.VersionID)
	if err != nil {
		httpx.WriteError(w, 404, "VERSION_NOT_FOUND", err.Error(), corrID())
		return
	}
	var req reduceRequest
	httpx.DecodeJSON(r, &req)
	algo := reducer.Algorithm(req.Algorithm)
	if algo == "" {
		algo = reducer.AlgoDDMin
	}

	if !a.orch.TryAcquire() {
		httpx.WriteError(w, 409, "STAND_BUSY", "another run is currently using the stand", corrID())
		return
	}
	defer a.orch.Release()

	var order func([]string) []string
	if algo == reducer.AlgoDependencyAware {
		deps := map[string][]string{}
		for _, iv := range sc.Interventions {
			deps[iv.ID] = iv.DependsOn
		}
		order = reducer.LeafFirstOrder(deps)
	}

	ctx := r.Context()
	reductionID := newID("red")
	exec := &candidateExecutor{o: a.orch, ctx: ctx, scenario: sc, versionID: run.VersionID, parentRunID: runID, reductionID: reductionID}
	budget := reducer.Budget{MaxRuns: sc.Budget.Runs, MaxDuration: time.Duration(sc.Budget.Seconds) * time.Second}

	result, err := reducer.Reduce(ctx, exec, run.ActiveIDs, algo, budget, order)
	if err != nil {
		httpx.WriteError(w, 500, "REDUCE_FAILED", err.Error(), corrID())
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{
		"reduction_id": reductionID,
		"algorithm":    result.Algorithm,
		"accepted_intervention_ids": result.Accepted,
		"runs_used":    result.RunsUsed,
		"stop_reason":  result.StopReason,
		"unstable":     result.Unstable,
	})
}

func (a *API) exportRun(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	runID := r.PathValue("id")
	run, err := a.store.GetRun(ctx, runID)
	if err != nil {
		httpx.WriteError(w, 404, "RUN_NOT_FOUND", err.Error(), corrID())
		return
	}
	events, err := a.store.ListEvents(ctx, runID, time.Time{}, 10000)
	if err != nil {
		httpx.WriteError(w, 500, "DB_ERROR", err.Error(), corrID())
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{
		"schema_version": schema.SchemaVersion,
		"run":            run,
		"events":         events, // already redacted by the proxy before being logged (NFR02)
	})
}

// ingestEvent receives event log entries POSTed by the proxy. This is an internal
// endpoint, not part of the public draft API in docs/07-architecture.md.
func (a *API) ingestEvent(w http.ResponseWriter, r *http.Request) {
	var ev schema.Event
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		httpx.WriteError(w, 400, "INVALID_BODY", err.Error(), corrID())
		return
	}
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now()
	}
	if err := a.store.InsertEvent(context.Background(), ev); err != nil {
		httpx.WriteError(w, 500, "DB_ERROR", err.Error(), corrID())
		return
	}
	httpx.WriteJSON(w, 200, nil)
}
