import type { Outcome, Run } from "./types";

// Minimum number of repeated executions before the UI will draw any conclusion about a
// candidate set (FR11: "нет вывода «исправлено» без повторов").
export const MIN_REPEATS_FOR_CONCLUSION = 3;

export interface RepeatSummary {
  total: number;
  counts: Record<string, number>; // keyed by outcome, or by state for non-COMPLETED runs
  verdict: "none" | "insufficient" | "no_failure_observed" | "failure_persists" | "uniform" | "mixed";
  text: string;
}

export function runKey(r: Run): string {
  return r.state === "COMPLETED" ? ((r.outcome as Outcome) || "COMPLETED") : r.state;
}

// Summarises repeated runs of one candidate set. It never says "fixed": at best it says
// no failure was observed in N runs, which is a statement about those runs only.
export function summarizeRepeats(runs: Run[], baseline: Run): RepeatSummary {
  const counts: Record<string, number> = {};
  for (const r of runs) counts[runKey(r)] = (counts[runKey(r)] ?? 0) + 1;
  const total = runs.length;
  const tally = Object.entries(counts)
    .map(([k, n]) => `${k} ${n}/${total}`)
    .join(", ");

  if (total === 0) return { total, counts, verdict: "none", text: "" };

  const allCompleted = runs.every((r) => r.state === "COMPLETED");
  const originalFails = counts["ORIGINAL_FAILURE"] ?? 0;
  const passes = counts["PASS"] ?? 0;

  if (total < MIN_REPEATS_FOR_CONCLUSION) {
    return {
      total,
      counts,
      verdict: "insufficient",
      text: `${tally}. ${total} run(s) is not enough to conclude anything; repeat at least ${MIN_REPEATS_FOR_CONCLUSION} times.`,
    };
  }
  if (!allCompleted) {
    return { total, counts, verdict: "mixed", text: `${tally}. Some runs did not reach a verdict, so no conclusion is drawn.` };
  }
  if (baseline.outcome === "ORIGINAL_FAILURE" && passes === total) {
    return {
      total,
      counts,
      verdict: "no_failure_observed",
      text: `${tally}. The original failure was not observed in any of ${total} runs of this set. This is evidence for these runs only, not proof that the defect is gone.`,
    };
  }
  if (originalFails === total) {
    return { total, counts, verdict: "failure_persists", text: `${tally}. The original failure reproduced every time with this set.` };
  }
  if (Object.keys(counts).length === 1) {
    // Every run agreed, but not in a way that says anything about the original failure
    // (e.g. the baseline did not fail either, or every run hit the same other failure).
    return { total, counts, verdict: "uniform", text: `${tally}. All ${total} runs gave the same result; compare it with the original run above.` };
  }
  return { total, counts, verdict: "mixed", text: `${tally}. Results are not uniform; the failure is flaky or another failure appeared.` };
}
