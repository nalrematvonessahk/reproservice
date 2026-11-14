import type { Outcome, RunState } from "./types";

// Human-readable explanation shown under the status badge. The point (per
// docs/08-ux.md) is that a reader must be able to tell these states apart without
// having read the architecture doc first.
export function explainRun(state: RunState | null, outcome?: Outcome): string {
  switch (state) {
    case null:
      return "Load a scenario and start a run to see results here.";
    case "VALIDATING":
      return "Checking the scenario before touching the stand.";
    case "RESETTING":
      return "Restoring the seed database and verifying image digests / health checks.";
    case "READY":
      return "Stand is reset and healthy; about to execute the checkout flow.";
    case "RUNNING":
      return "Checkout flow is executing against the stand with the selected faults active.";
    case "ASSERTING":
      return "Flow finished; the oracle is checking the invariant.";
    case "CANCELLED":
      return "Run was cancelled by request before it reached a verdict. Not a pass, not a failure -- no verdict was produced.";
    case "INFRA_ERROR":
      return "The stand itself failed (health check, reset, or container problem) -- this is NOT an invariant violation. The scenario's fault logic was never meaningfully exercised.";
    case "COMPLETED":
      switch (outcome) {
        case "PASS":
          return "Flow completed and the invariant held. No violation detected under the active interventions.";
        case "ORIGINAL_FAILURE":
          return "The invariant was violated, matching the scenario's declared invariant_id. This is the bug you configured the scenario to reproduce.";
        case "OTHER_FAILURE":
          return "Something failed, but it does not match the scenario's declared invariant -- a different problem than the one being investigated.";
        case "TIMEOUT":
          return "An expected interaction did not complete within the time budget. Distinct from the declared invariant violation unless it is itself the invariant being checked.";
        case "INAPPLICABLE":
          return "An intervention's selector never matched any event in the time window -- the fault was never exercised, so no verdict about the invariant can be drawn from this run.";
        default:
          return "Run completed.";
      }
    default:
      return "";
  }
}

export function explainStopReason(reason: string): string {
  switch (reason) {
    case "minimal":
      return "Every single-intervention removal from the accepted set was tried and each one failed to reproduce the original failure under the same conditions -- this is 1-minimal, not a claimed global minimum.";
    case "BUDGET_EXHAUSTED_RUNS":
      return "Reduction stopped because it used all of the scenario's run budget before reaching a 1-minimal set. The result below is the best confirmed candidate found so far, not a final minimal set.";
    case "BUDGET_EXHAUSTED_SECONDS":
      return "Reduction stopped because it used all of the scenario's time budget before reaching a 1-minimal set. The result below is the best confirmed candidate found so far, not a final minimal set.";
    case "UNSTABLE_ORIGINAL":
      return "The full intervention set did not reproduce the original failure consistently (flaky), so reduction could not proceed safely.";
    case "ORIGINAL_NOT_REPRODUCED":
      return "The active intervention set never reproduced ORIGINAL_FAILURE in the first place, so there is nothing to reduce.";
    case "CANCELLED":
      return "Reduction was cancelled before it finished.";
    default:
      return reason;
  }
}
