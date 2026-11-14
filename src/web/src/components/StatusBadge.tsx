import type { Outcome, RunState } from "../types";

// Central place mapping run state/outcome to a visually distinct badge. Per
// docs/08-ux.md every one of these must read as a different state at a glance:
// empty, running, cancelled, original failure, other failure, infra error,
// inapplicable -- not just a generic "done"/"error".
const WORKING_STATES: RunState[] = ["VALIDATING", "RESETTING", "READY", "RUNNING", "ASSERTING"];

export function badgeForRun(state: RunState | null, outcome?: Outcome): { text: string; tone: string } {
  if (!state) return { text: "no run yet", tone: "tone-neutral" };
  if (WORKING_STATES.includes(state)) return { text: state, tone: "tone-progress" };
  if (state === "CANCELLED") return { text: "CANCELLED", tone: "tone-cancelled" };
  if (state === "INFRA_ERROR") return { text: "INFRA_ERROR", tone: "tone-infra" };
  if (state === "COMPLETED") {
    switch (outcome) {
      case "PASS":
        return { text: "PASS", tone: "tone-pass" };
      case "ORIGINAL_FAILURE":
        return { text: "ORIGINAL_FAILURE", tone: "tone-original-failure" };
      case "OTHER_FAILURE":
        return { text: "OTHER_FAILURE", tone: "tone-other-failure" };
      case "TIMEOUT":
        return { text: "TIMEOUT", tone: "tone-timeout" };
      case "INAPPLICABLE":
        return { text: "INAPPLICABLE", tone: "tone-inapplicable" };
      default:
        return { text: "COMPLETED", tone: "tone-neutral" };
    }
  }
  return { text: state, tone: "tone-neutral" };
}

export default function StatusBadge({ state, outcome }: { state: RunState | null; outcome?: Outcome }) {
  const { text, tone } = badgeForRun(state, outcome);
  return (
    <span className={`badge ${tone}`} role="status" aria-live="polite">
      {text}
    </span>
  );
}
