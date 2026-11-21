import { describe, expect, it } from "vitest";
import { summarizeRepeats } from "./compare";
import { run } from "./fixtures";

const failed = run({ outcome: "ORIGINAL_FAILURE" });
const pass = (n: number) => Array.from({ length: n }, (_, i) => run({ run_id: `r${i}`, outcome: "PASS" }));

describe("summarizeRepeats (FR11: no 'fixed' conclusion without repeats)", () => {
  it("draws no conclusion from a single run", () => {
    const s = summarizeRepeats(pass(1), failed);
    expect(s.verdict).toBe("insufficient");
    expect(s.text).toMatch(/not enough/);
  });
  it("draws no conclusion from two runs", () => {
    expect(summarizeRepeats(pass(2), failed).verdict).toBe("insufficient");
  });
  it("reports 'no failure observed' (never 'fixed') after 3/3 passes", () => {
    const s = summarizeRepeats(pass(3), failed);
    expect(s.verdict).toBe("no_failure_observed");
    expect(s.text).toMatch(/PASS 3\/3/);
    expect(s.text.toLowerCase()).not.toContain("fixed");
    expect(s.text).toMatch(/not proof/);
  });
  it("is mixed when results differ", () => {
    const runs = [...pass(2), run({ run_id: "x", outcome: "ORIGINAL_FAILURE" })];
    expect(summarizeRepeats(runs, failed).verdict).toBe("mixed");
  });
  it("reports persistence when the failure reproduces every time", () => {
    const runs = Array.from({ length: 3 }, (_, i) => run({ run_id: `r${i}`, outcome: "ORIGINAL_FAILURE" }));
    expect(summarizeRepeats(runs, failed).verdict).toBe("failure_persists");
  });
  it("does not conclude when a run was cancelled or hit an infra error", () => {
    const runs = [...pass(2), run({ run_id: "c", state: "CANCELLED", outcome: "" })];
    const s = summarizeRepeats(runs, failed);
    expect(s.verdict).toBe("mixed");
    expect(s.counts["CANCELLED"]).toBe(1);
  });
  it("does not claim a fix when the original run did not fail", () => {
    expect(summarizeRepeats(pass(3), run({ outcome: "PASS" })).verdict).toBe("mixed");
  });
  it("returns an empty summary for no runs", () => {
    expect(summarizeRepeats([], failed).text).toBe("");
  });
});
