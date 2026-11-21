import type {
  ApiErrorBody,
  ExportPayload,
  ReduceAlgorithm,
  ReduceResult,
  Run,
  RunEvent,
} from "./types";
import { isTerminalState } from "./types";

// Controller base URL. The controller enables CORS for localhost/127.0.0.1 origins,
// so the dev server (any port) can call it directly -- no proxy needed.
export const API_BASE =
  (import.meta as unknown as { env?: { VITE_API_BASE?: string } }).env?.VITE_API_BASE ??
  "http://127.0.0.1:7000";

export class ApiError extends Error {
  status: number;
  body: ApiErrorBody;
  constructor(status: number, body: ApiErrorBody) {
    super(body.message || `HTTP ${status}`);
    this.status = status;
    this.body = body;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  try {
    res = await fetch(`${API_BASE}${path}`, {
      ...init,
      headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
    });
  } catch (e) {
    // Network-level failure (controller unreachable, CORS, DNS, etc) -- distinguish
    // this from an API-level error so the UI can show "cannot reach controller".
    throw new ApiError(0, {
      code: "NETWORK_ERROR",
      message: e instanceof Error ? e.message : "network request failed",
      correlation_id: "",
    });
  }
  if (!res.ok) {
    // Read the body once as text: calling res.json() first and res.text() after a
    // parse failure throws "body already read" and loses the real error message.
    const text = await res.text().catch(() => "");
    throw new ApiError(res.status, parseErrorBody(res.status, res.statusText, text));
  }
  if (res.status === 204) return undefined as T;
  try {
    return (await res.json()) as T;
  } catch {
    throw new ApiError(res.status, {
      code: "INVALID_RESPONSE",
      message: `controller returned a non-JSON body for ${path}`,
      correlation_id: "",
    });
  }
}

// The controller's error envelope is {code, message, field?, correlation_id}. Anything
// else (a proxy's HTML 502 page, an empty body) is still turned into a usable error.
export function parseErrorBody(status: number, statusText: string, text: string): ApiErrorBody {
  try {
    const j = JSON.parse(text) as Partial<ApiErrorBody> | null;
    if (j && typeof j === "object" && typeof j.code === "string") {
      return {
        code: j.code,
        message: j.message || statusText || `HTTP ${status}`,
        field: j.field,
        correlation_id: j.correlation_id ?? "",
      };
    }
  } catch {
    // not JSON -- fall through
  }
  return {
    code: "HTTP_" + status,
    message: text.trim().slice(0, 300) || statusText || `HTTP ${status}`,
    correlation_id: "",
  };
}

// A failure worth retrying: the controller was unreachable (status 0) or answered 5xx.
export function isTransientError(e: unknown): boolean {
  return e instanceof ApiError && (e.status === 0 || e.status >= 500);
}

export const MAX_TRANSIENT_FAILURES = 5;

export interface ErrorView {
  code: string;
  message: string;
  field?: string;
  correlation_id: string;
}

// One place that turns anything thrown by the API client into something renderable.
export function describeError(e: unknown): ErrorView {
  if (e instanceof ApiError) {
    return { code: e.body.code, message: e.body.message, field: e.body.field, correlation_id: e.body.correlation_id };
  }
  return { code: "UNKNOWN_ERROR", message: e instanceof Error ? e.message : String(e), correlation_id: "" };
}

export interface CreateScenarioResponse {
  schema_version: number;
  version_id: string;
  scenario_id: string;
}

export function createScenario(scenarioJson: unknown): Promise<CreateScenarioResponse> {
  return request<CreateScenarioResponse>("/api/scenarios", {
    method: "POST",
    body: JSON.stringify(scenarioJson),
  });
}

export interface CreateRunRequest {
  version_id: string;
  idempotency_key: string;
  active_intervention_ids?: string[];
}

export interface CreateRunAccepted {
  run_id: string;
}

// Returns either the 202 {run_id} shape or the 200 full Run (idempotent replay).
export function createRun(body: CreateRunRequest): Promise<CreateRunAccepted | Run> {
  return request<CreateRunAccepted | Run>("/api/runs", {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export async function getRun(runId: string): Promise<Run> {
  const run = await request<Run>(`/api/runs/${encodeURIComponent(runId)}`);
  return { ...run, active_intervention_ids: run.active_intervention_ids ?? [] };
}

export interface ListEventsResponse {
  events: RunEvent[];
  cursor: string;
}

// The controller encodes "no events" as `"events": null` (a nil Go slice), not [].
export async function listEvents(runId: string, cursor?: string): Promise<ListEventsResponse> {
  const qs = cursor ? `?cursor=${encodeURIComponent(cursor)}` : "";
  const res = await request<Partial<ListEventsResponse>>(`/api/runs/${encodeURIComponent(runId)}/events${qs}`);
  return { events: res.events ?? [], cursor: res.cursor ?? "" };
}

export function cancelRun(runId: string): Promise<{ status: string }> {
  return request(`/api/runs/${encodeURIComponent(runId)}/cancel`, { method: "POST" });
}

export async function reduceRun(runId: string, algorithm: ReduceAlgorithm): Promise<ReduceResult> {
  const res = await request<ReduceResult>(`/api/runs/${encodeURIComponent(runId)}/reduce`, {
    method: "POST",
    body: JSON.stringify({ algorithm }),
  });
  // schema.Reduction.Accepted is a Go slice: nil is serialised as null.
  return { ...res, accepted_intervention_ids: res.accepted_intervention_ids ?? [] };
}

// events and active_intervention_ids are Go slices and arrive as null when empty.
export async function exportRun(runId: string): Promise<ExportPayload> {
  const res = await request<ExportPayload>(`/api/runs/${encodeURIComponent(runId)}/export`);
  return {
    ...res,
    run: { ...res.run, active_intervention_ids: res.run.active_intervention_ids ?? [] },
    events: res.events ?? [],
  };
}

// Polls GET /api/runs/{id} until the run is terminal (used for sequential repeats).
export async function waitForRun(runId: string, opts: { signal?: AbortSignal; intervalMs?: number } = {}): Promise<Run> {
  const interval = opts.intervalMs ?? 350;
  let failures = 0;
  for (;;) {
    if (opts.signal?.aborted) throw new DOMException("aborted", "AbortError");
    try {
      const run = await getRun(runId);
      failures = 0;
      if (isTerminalState(run.state)) return run;
    } catch (e) {
      // Same tolerance as the main run view: ride out a short controller restart.
      if (!isTransientError(e) || ++failures >= MAX_TRANSIENT_FAILURES) throw e;
    }
    await new Promise((r) => setTimeout(r, interval * Math.max(1, failures)));
  }
}

// POST /api/runs answers 202 {run_id} for a new run and 200 with the full Run when the
// idempotency key was already used.
export function isExistingRun(r: CreateRunAccepted | Run): r is Run {
  return "state" in r;
}
