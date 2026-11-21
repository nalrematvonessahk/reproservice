import type { Run, RunEvent, Scenario } from "./types";

export function ev(id: string, over: Partial<RunEvent> = {}): RunEvent {
  return {
    event_id: id,
    run_id: "run-1",
    logical_operation: "checkout-001",
    source: "orders",
    target: "payments",
    method: "POST",
    normalized_path: "/charge",
    attempt: 1,
    monotonic_offset_ms: 1,
    phase: "response",
    result: "forwarded",
    created_at: "2026-11-21T17:11:08.10449Z",
    ...over,
  };
}

export function run(over: Partial<Run> = {}): Run {
  return {
    run_id: "run-1",
    scenario_id: "s",
    version_id: "ver-1",
    idempotency_key: "k",
    state: "COMPLETED",
    outcome: "PASS",
    active_intervention_ids: [],
    created_at: "2026-11-21T17:11:07Z",
    updated_at: "2026-11-21T17:11:08Z",
    ...over,
  };
}

export const scenarioFixture: Scenario = {
  schema_version: 1,
  id: "double-payment-demo",
  environment: { compose_profile: "orders-demo", seed_id: "seed-001" },
  invariant_id: "payment_exactly_once",
  budget: { runs: 100, seconds: 600 },
  interventions: [
    {
      id: "drop-payments-response",
      selector: {
        logical_operation: "checkout-001",
        source: "orders",
        target: "payments",
        method: "POST",
        normalized_path: "/charge",
        attempt: 1,
      },
      action: { type: "drop_response_after_success" },
      depends_on: [],
    },
    {
      id: "delay-inventory",
      selector: {
        logical_operation: "checkout-001",
        source: "orders",
        target: "inventory",
        method: "POST",
        normalized_path: "/reserve",
        attempt: 1,
      },
      action: { type: "delay", delay_ms: 200 },
      depends_on: ["drop-payments-response"],
    },
  ],
};
