// proxy sits between the demo SUT services and implements the HTTP-level fault
// injection described in docs/07-architecture.md and ADR-001: it completes the
// request to upstream, logs the event, THEN applies the fault action downstream --
// this ordering is what makes "lose a successful response" (FR05) possible.
//
// Routing convention: a request path /{target}/{rest...} is forwarded to the
// upstream base URL registered for {target} (see -routes flag / ROUTES env var).
// The caller must set X-Logical-Operation, X-Source and X-Attempt headers; these,
// plus the path and method, form the Selector key used to match active interventions
// (ADR-002). X-Run-Id scopes matching to the currently active run; traffic for any
// other run (or no run) passes through unfaulted.
//
// Admin API (loopback-only by convention; see NFR01 for the controller, which is the
// only piece required to bind 127.0.0.1 -- the proxy's admin port is configured the
// same way in deploy/docker-compose.yml): the controller calls POST /admin/run to
// activate a run's intervention set before RUNNING, and DELETE /admin/run to clear it.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"reproservice/internal/httpx"
	"reproservice/internal/schema"
)

// activeRun holds the single run currently allowed to have faults applied, matching
// the architecture's "занятый стенд" (busy stand) model: one run at a time.
type activeRun struct {
	mu            sync.Mutex
	runID         string
	interventions map[string]schema.Intervention // keyed by Selector.Key()
	fired         map[string]int                 // intervention ID -> number of selector matches this run (FR03)
}

func (a *activeRun) activate(runID string, ivs []schema.Intervention) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.runID != "" && a.runID != runID {
		return fmt.Errorf("stand busy with run %s", a.runID)
	}
	m := make(map[string]schema.Intervention, len(ivs))
	for _, iv := range ivs {
		m[iv.Selector.Key()] = iv
	}
	a.runID = runID
	a.interventions = m
	a.fired = make(map[string]int, len(ivs))
	for _, iv := range ivs {
		a.fired[iv.ID] = 0
	}
	return nil
}

func (a *activeRun) deactivate(runID string) error {
	_, err := a.finish(runID)
	return err
}

// finish ends the run's fault scope and returns how many times each activated
// intervention's selector matched (0 = the event never occurred, >1 = ambiguous);
// the controller turns either into INAPPLICABLE.
func (a *activeRun) finish(runID string) (map[string]int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.runID != runID {
		return nil, fmt.Errorf("run %s is not the active run", runID)
	}
	fired := a.fired
	a.runID = ""
	a.interventions = nil
	a.fired = nil
	return fired, nil
}

func (a *activeRun) lookup(runID string, sel schema.Selector) (schema.Intervention, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.runID == "" || a.runID != runID {
		return schema.Intervention{}, false
	}
	iv, ok := a.interventions[sel.Key()]
	if ok {
		a.fired[iv.ID]++
	}
	return iv, ok
}

type eventSink struct {
	controllerURL string
	client        *http.Client
}

func (s *eventSink) emit(ev schema.Event) {
	if s.controllerURL == "" {
		return
	}
	b, _ := json.Marshal(ev)
	url := strings.TrimRight(s.controllerURL, "/") + "/internal/runs/" + ev.RunID + "/events"
	resp, err := s.client.Post(url, "application/json", strings.NewReader(string(b)))
	if err != nil {
		log.Printf("proxy: emit event: %v", err)
		return
	}
	resp.Body.Close()
}

var sensitiveHeaders = map[string]bool{
	"authorization": true,
	"cookie":        true,
	"set-cookie":    true,
}

func redact(h http.Header) {
	for name := range h {
		if sensitiveHeaders[strings.ToLower(name)] {
			h.Set(name, "[redacted]")
		}
	}
}

type server struct {
	routes    map[string]*url.URL
	run       *activeRun
	sink      *eventSink
	startedAt time.Time
}

func (s *server) handleProxy(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	if len(parts) < 1 || parts[0] == "" {
		httpx.WriteError(w, 400, "BAD_ROUTE", "path must start with /{target}/...", "")
		return
	}
	target := parts[0]
	rest := "/"
	if len(parts) == 2 {
		rest = "/" + parts[1]
	}
	upstream, ok := s.routes[target]
	if !ok {
		httpx.WriteError(w, 404, "UNKNOWN_TARGET", fmt.Sprintf("no upstream registered for %q", target), "target")
		return
	}

	redact(r.Header)
	logicalOp := r.Header.Get("X-Logical-Operation")
	source := r.Header.Get("X-Source")
	runID := r.Header.Get("X-Run-Id")
	attempt, _ := strconv.Atoi(r.Header.Get("X-Attempt"))
	if attempt == 0 {
		attempt = 1
	}

	sel := schema.Selector{
		LogicalOperation: logicalOp,
		Source:           source,
		Target:           target,
		Method:           r.Method,
		NormalizedPath:   rest,
		Attempt:          attempt,
	}

	iv, matched := s.run.lookup(runID, sel)

	offset := time.Since(s.startedAt).Milliseconds()
	eventID := newID()

	if matched && iv.Action.Type == schema.ActionHTTPError {
		// FR04: the error is synthesized before the request ever reaches upstream.
		s.sink.emit(schema.Event{
			EventID: eventID, RunID: runID, LogicalOperation: logicalOp, Source: source,
			Target: target, Method: r.Method, NormalizedPath: rest, Attempt: attempt,
			MonotonicOffsetMs: offset, ActionID: iv.ID, Phase: schema.PhaseFault,
			Result: schema.ResultFaulted, StatusCode: iv.Action.StatusCode, CreatedAt: time.Now(),
		})
		httpx.WriteError(w, iv.Action.StatusCode, "INJECTED_FAULT", "synthesized upstream error", "")
		return
	}

	if matched && iv.Action.Type == schema.ActionDelay {
		time.Sleep(time.Duration(iv.Action.DelayMs) * time.Millisecond)
	}

	if matched && iv.Action.Type == schema.ActionDropResponseAfterSuccess {
		s.proxyAndMaybeDrop(w, r, upstream, rest, eventID, runID, logicalOp, source, target, attempt, offset, iv.ID)
		return
	}

	// Plain forward (including the delay case, after sleeping above).
	status := s.forward(w, r, upstream, rest)
	s.sink.emit(schema.Event{
		EventID: eventID, RunID: runID, LogicalOperation: logicalOp, Source: source,
		Target: target, Method: r.Method, NormalizedPath: rest, Attempt: attempt,
		MonotonicOffsetMs: offset, Phase: schema.PhaseResponse, Result: schema.ResultForwarded,
		StatusCode: status, CreatedAt: time.Now(),
	})
}

// proxyAndMaybeDrop implements ADR-001 literally: it completes the call to upstream
// itself (not via the streaming reverse proxy, so it can inspect the status code),
// logs the successful completion, and only then decides whether to relay the response
// to the caller or hijack the connection and close it without writing anything.
func (s *server) proxyAndMaybeDrop(w http.ResponseWriter, r *http.Request, upstream *url.URL, rest, eventID, runID, logicalOp, source, target string, attempt int, offset int64, actionID string) {
	body, _ := io.ReadAll(r.Body)
	outReq, err := http.NewRequest(r.Method, upstream.String()+rest, strings.NewReader(string(body)))
	if err != nil {
		httpx.WriteError(w, 502, "BAD_UPSTREAM_REQUEST", err.Error(), "")
		return
	}
	outReq.Header = r.Header.Clone()

	resp, err := http.DefaultClient.Do(outReq)
	if err != nil {
		httpx.WriteError(w, 502, "UPSTREAM_UNAVAILABLE", err.Error(), "")
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	s.sink.emit(schema.Event{
		EventID: eventID, RunID: runID, LogicalOperation: logicalOp, Source: source,
		Target: target, Method: r.Method, NormalizedPath: rest, Attempt: attempt,
		MonotonicOffsetMs: offset, Phase: schema.PhaseResponse, Result: schema.ResultForwarded,
		StatusCode: resp.StatusCode, CreatedAt: time.Now(),
	})

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Action only fires "after success" (FR05); forward failures through untouched.
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
		return
	}

	dropEventID := newID()
	s.sink.emit(schema.Event{
		EventID: dropEventID, RunID: runID, LogicalOperation: logicalOp, Source: source,
		Target: target, Method: r.Method, NormalizedPath: rest, Attempt: attempt,
		ParentEventID: eventID, MonotonicOffsetMs: time.Since(s.startedAt).Milliseconds(),
		ActionID: actionID, Phase: schema.PhaseFault, Result: schema.ResultDropped,
		StatusCode: resp.StatusCode, CreatedAt: time.Now(),
	})

	hj, ok := w.(http.Hijacker)
	if !ok {
		// Fallback for response writers that can't be hijacked: closest approximation
		// is an abrupt non-2xx with no body, which still breaks the caller's happy path.
		w.WriteHeader(599)
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		w.WriteHeader(599)
		return
	}
	conn.Close() // the operation succeeded upstream, but the caller never finds out
}

func (s *server) forward(w http.ResponseWriter, r *http.Request, upstream *url.URL, rest string) int {
	rec := &statusRecorder{ResponseWriter: w, status: 200}
	rp := httputil.NewSingleHostReverseProxy(upstream)
	rp.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
		httpx.WriteError(rw, 502, "UPSTREAM_UNAVAILABLE", err.Error(), "")
		rec.status = 502
	}
	r.URL.Path = singleJoiningSlash(upstream.Path, rest)
	rp.ServeHTTP(rec, r)
	return rec.status
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

type activateRequest struct {
	Interventions []schema.Intervention `json:"interventions"`
}

func (s *server) handleAdminActivate(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	var req activateRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, 400, "INVALID_BODY", err.Error(), "")
		return
	}
	if err := s.run.activate(runID, req.Interventions); err != nil {
		httpx.WriteError(w, 409, "STAND_BUSY", err.Error(), "")
		return
	}
	httpx.WriteJSON(w, 200, map[string]string{"status": "activated"})
}

func (s *server) handleAdminDeactivate(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	fired, err := s.run.finish(runID)
	if err != nil {
		httpx.WriteError(w, 409, "NOT_ACTIVE_RUN", err.Error(), "")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"status": "deactivated", "fired": fired})
}

func main() {
	routesEnv := envDefault("ROUTES", "payments=http://localhost:8082,inventory=http://localhost:8083")
	routes, err := parseRoutes(routesEnv)
	if err != nil {
		log.Fatalf("proxy: %v", err)
	}
	s := &server{
		routes:    routes,
		run:       &activeRun{},
		sink:      &eventSink{controllerURL: os.Getenv("CONTROLLER_INTERNAL_URL"), client: &http.Client{Timeout: 2 * time.Second}},
		startedAt: time.Now(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { httpx.WriteJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("POST /admin/runs/{run_id}/activate", s.handleAdminActivate)
	mux.HandleFunc("DELETE /admin/runs/{run_id}/activate", s.handleAdminDeactivate)
	mux.HandleFunc("/", s.handleProxy)

	addr := envDefault("LISTEN_ADDR", ":9000")
	log.Printf("proxy listening on %s, routes=%v", addr, routesKeys(routes))
	log.Fatal(http.ListenAndServe(addr, mux))
}

func parseRoutes(spec string) (map[string]*url.URL, error) {
	out := map[string]*url.URL{}
	for _, pair := range strings.Split(spec, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid route entry %q", pair)
		}
		u, err := url.Parse(kv[1])
		if err != nil {
			return nil, fmt.Errorf("invalid route url %q: %w", kv[1], err)
		}
		out[kv[0]] = u
	}
	return out, nil
}

func routesKeys(m map[string]*url.URL) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

var idCounter struct {
	sync.Mutex
	n int64
}

// newID produces a locally-unique, time-ordered event id. It is intentionally not a
// random trace id -- ADR-002 requires the replay key to be the Selector, not an id.
func newID() string {
	idCounter.Lock()
	idCounter.n++
	n := idCounter.n
	idCounter.Unlock()
	return fmt.Sprintf("ev-%d-%d", time.Now().UnixNano(), n)
}
