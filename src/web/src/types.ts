// Mirrors internal/schema/*.go. Keep in sync with the Go structs — this is a thin
// client for the real controller API, not an independent data model.

export type ActionType = "delay" | "http_error" | "drop_response_after_success";

export interface Selector {
  logical_operation: string;
  source: string;
  target: string;
  method: string;
  normalized_path: string;
  attempt: number;
}

export interface Action {
  type: ActionType;
  delay_ms?: number;
  status_code?: number;
}

export interface Intervention {
  id: string;
  selector: Selector;
  action: Action;
  depends_on: string[];
}

export interface Environment {
  compose_profile: string;
  image_digests?: Record<string, string>;
  seed_id: string;
  seed_hash?: string;
}

export interface Budget {
  runs: number;
  seconds: number;
}

export interface Scenario {
  schema_version: number;
  id: string;
  kind?: string;
  environment: Environment;
  invariant_id: string;
  budget: Budget;
  interventions: Intervention[];
}

export type RunState =
  | "VALIDATING"
  | "RESETTING"
  | "READY"
  | "RUNNING"
  | "ASSERTING"
  | "COMPLETED"
  | "CANCELLED"
  | "INFRA_ERROR";

export type Outcome = "PASS" | "ORIGINAL_FAILURE" | "OTHER_FAILURE" | "TIMEOUT" | "INAPPLICABLE" | "";

export interface Run {
  run_id: string;
  scenario_id: string;
  version_id: string;
  idempotency_key: string;
  state: RunState;
  outcome?: Outcome;
  stop_reason?: string;
  active_intervention_ids: string[];
  created_at: string;
  updated_at: string;
}

export type EventPhase = "request" | "response" | "fault";
export type EventResult = "forwarded" | "faulted" | "dropped";

export interface RunEvent {
  event_id: string;
  run_id: string;
  logical_operation: string;
  source: string;
  target: string;
  method: string;
  normalized_path: string;
  attempt: number;
  parent_event_id?: string;
  monotonic_offset_ms: number;
  action_id?: string;
  phase: EventPhase;
  result: EventResult;
  status_code?: number;
  created_at: string;
}

export type ReduceAlgorithm = "ddmin" | "sequential" | "dependency_aware";

// Mirrors schema.Reduction (internal/schema/run.go), the body of POST
// /api/runs/{id}/reduce and GET /api/reductions/{id}. The fields below the first
// block are optional in the type because older controller builds did not send them.
export interface ReduceResult {
  reduction_id: string;
  run_id?: string;
  algorithm: ReduceAlgorithm;
  state?: string;
  accepted_intervention_ids: string[];
  runs_used: number;
  stop_reason: string;
  unstable: boolean;
  confirmed?: boolean;
  one_minimal?: boolean;
  max_runs?: number;
  max_seconds?: number;
  confirmation_runs?: number;
  cache_hits?: number;
  skipped_invalid?: number;
  error?: string;
  duration_ms?: number;
}

// Body of GET /api/runs/{id}/export, exactly as the controller sends it.
export interface ExportPayload {
  schema_version: number;
  run: Run;
  events: RunEvent[];
}

// What the UI saves to disk (FR12): the controller payload plus the scenario
// configuration the run was started from and, if one was made, the reduction result.
// The controller's export has no configuration section, so the UI adds it.
export interface ExportDocument extends ExportPayload {
  scenario: Scenario;
  reduction?: ReduceResult;
}

export const TERMINAL_STATES: readonly RunState[] = ["COMPLETED", "CANCELLED", "INFRA_ERROR"];

export function isTerminalState(state: RunState | string | null | undefined): boolean {
  return state != null && (TERMINAL_STATES as readonly string[]).includes(state);
}

export interface ApiErrorBody {
  code: string;
  message: string;
  field?: string;
  correlation_id: string;
}
