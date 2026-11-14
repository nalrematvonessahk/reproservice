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

export interface ReduceResult {
  reduction_id: string;
  algorithm: ReduceAlgorithm;
  accepted_intervention_ids: string[];
  runs_used: number;
  stop_reason: string;
  unstable: boolean;
}

export interface ExportPayload {
  schema_version: number;
  run: Run;
  events: RunEvent[];
}

export interface ApiErrorBody {
  code: string;
  message: string;
  field?: string;
  correlation_id: string;
}
