// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import StatusBadge, { badgeForRun } from "./StatusBadge";
import type { Outcome, RunState } from "../types";

describe("badgeForRun", () => {
  it("shows a neutral badge before any run exists", () => {
    expect(badgeForRun(null)).toEqual({ text: "no run yet", tone: "tone-neutral" });
  });

  it.each(["VALIDATING", "RESETTING", "READY", "RUNNING", "ASSERTING"] as RunState[])(
    "marks %s as in progress",
    (state) => {
      expect(badgeForRun(state)).toEqual({ text: state, tone: "tone-progress" });
    }
  );

  it.each([
    ["PASS", "tone-pass"],
    ["ORIGINAL_FAILURE", "tone-original-failure"],
    ["OTHER_FAILURE", "tone-other-failure"],
    ["TIMEOUT", "tone-timeout"],
    ["INAPPLICABLE", "tone-inapplicable"],
  ] as [Outcome, string][])("gives outcome %s its own tone", (outcome, tone) => {
    expect(badgeForRun("COMPLETED", outcome)).toEqual({ text: outcome, tone });
  });

  it("uses distinct tones for every terminal state and outcome", () => {
    const tones = new Set([
      badgeForRun("CANCELLED").tone,
      badgeForRun("INFRA_ERROR").tone,
      badgeForRun("COMPLETED", "PASS").tone,
      badgeForRun("COMPLETED", "ORIGINAL_FAILURE").tone,
      badgeForRun("COMPLETED", "OTHER_FAILURE").tone,
      badgeForRun("COMPLETED", "TIMEOUT").tone,
      badgeForRun("COMPLETED", "INAPPLICABLE").tone,
    ]);
    expect(tones.size).toBe(7);
  });

  it("falls back to COMPLETED when the outcome is empty", () => {
    expect(badgeForRun("COMPLETED", "")).toEqual({ text: "COMPLETED", tone: "tone-neutral" });
  });
});

describe("<StatusBadge/>", () => {
  it("is a polite live status region carrying the outcome text", () => {
    render(<StatusBadge state="COMPLETED" outcome="ORIGINAL_FAILURE" />);
    const badge = screen.getByRole("status");
    expect(badge).toHaveTextContent("ORIGINAL_FAILURE");
    expect(badge).toHaveAttribute("aria-live", "polite");
    expect(badge).toHaveClass("tone-original-failure");
  });
});
