import type {
  ApiErrorBody,
  ExportPayload,
  ReduceAlgorithm,
  ReduceResult,
  Run,
  RunEvent,
} from "./types";

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
    let body: ApiErrorBody;
    try {
      body = await res.json();
    } catch {
      body = { code: "UNKNOWN_ERROR", message: await res.text().catch(() => res.statusText), correlation_id: "" };
    }
    throw new ApiError(res.status, body);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
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

export function getRun(runId: string): Promise<Run> {
  return request<Run>(`/api/runs/${runId}`);
}

export interface ListEventsResponse {
  events: RunEvent[];
  cursor: string;
}

export function listEvents(runId: string, cursor?: string): Promise<ListEventsResponse> {
  const qs = cursor ? `?cursor=${encodeURIComponent(cursor)}` : "";
  return request<ListEventsResponse>(`/api/runs/${runId}/events${qs}`);
}

export function cancelRun(runId: string): Promise<{ status: string }> {
  return request(`/api/runs/${runId}/cancel`, { method: "POST" });
}

export function reduceRun(runId: string, algorithm: ReduceAlgorithm): Promise<ReduceResult> {
  return request<ReduceResult>(`/api/runs/${runId}/reduce`, {
    method: "POST",
    body: JSON.stringify({ algorithm }),
  });
}

export function exportRun(runId: string): Promise<ExportPayload> {
  return request<ExportPayload>(`/api/runs/${runId}/export`);
}

export function isCreateRunAccepted(r: CreateRunAccepted | Run): r is CreateRunAccepted {
  return !("state" in r);
}
