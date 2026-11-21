// @vitest-environment jsdom
import { readFileSync } from "node:fs";
import axe from "axe-core";
import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import EventTimeline from "./components/EventTimeline";
import ExportPanel from "./components/ExportPanel";
import ReductionPanel from "./components/ReductionPanel";
import RunPanel from "./components/RunPanel";
import { ev, run, scenarioFixture } from "./fixtures";
import type { ReduceResult } from "./types";

async function violations(node: Element) {
  const result = await axe.run(node, {
    rules: { "color-contrast": { enabled: false }, region: { enabled: false } },
  });
  return result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`);
}

const reduction: ReduceResult = {
  reduction_id: "r1",
  algorithm: "ddmin",
  accepted_intervention_ids: ["drop-payments-response"],
  runs_used: 7,
  stop_reason: "minimal",
  unstable: false,
  confirmed: true,
  one_minimal: true,
  max_runs: 100,
  confirmation_runs: 3,
};

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("page structure", () => {
  it("declares the page language as English", () => {
    const html = readFileSync("index.html", "utf8");
    expect(html).toMatch(/<html lang="en">/);
  });

  it("has one level-one heading, a labelled controller note, main and footer landmarks", () => {
    render(<App />);
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("ReproService");
    expect(screen.getByRole("complementary", { name: "Controller address" })).toBeInTheDocument();
    expect(screen.getByRole("main")).toBeInTheDocument();
    expect(screen.getByRole("contentinfo")).toBeInTheDocument();
    expect(screen.getByRole("banner")).toBeInTheDocument();
  });
});

describe("axe-core", () => {
  it("finds no violations in the empty page", async () => {
    const { container } = render(<App />);
    expect(await violations(container)).toEqual([]);
  });

  it("finds no violations in the run panel with a finished run and its raw JSON", async () => {
    const { container } = render(
      <RunPanel
        scenario={scenarioFixture}
        versionId="ver-1"
        activeIds={new Set(["drop-payments-response"])}
        onToggleIntervention={() => {}}
        idempotencyKey="k"
        onIdempotencyKeyChange={() => {}}
        onStart={() => {}}
        onCancel={() => {}}
        starting={false}
        run={run({ outcome: "ORIGINAL_FAILURE" })}
        runError={{ code: "STAND_BUSY", message: "busy", correlation_id: "c" }}
        notice="replay"
      />
    );
    screen.getByRole("button", { name: "Show raw run JSON" }).click();
    expect(await violations(container)).toEqual([]);
  });

  it("finds no violations in the timeline with a selected event", async () => {
    const events = [ev("e1"), ev("e2", { phase: "fault", result: "dropped", action_id: "drop-payments-response", parent_event_id: "e1" })];
    const { container } = render(<EventTimeline events={events} scenario={scenarioFixture} />);
    screen.getByRole("button", { name: /fault\/dropped/ }).click();
    expect(await violations(container)).toEqual([]);
  });

  it("finds no violations in the reduction panel with a result", async () => {
    const { container } = render(
      <ReductionPanel run={run({ outcome: "ORIGINAL_FAILURE" })} onReduce={() => {}} onCancel={() => {}} busy={false} result={reduction} error={null} />
    );
    expect(await violations(container)).toEqual([]);
  });

  it("finds no violations in the export panel with a preview", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_input: RequestInfo | URL) => new Response(JSON.stringify({ schema_version: 1, run: run(), events: [ev("e1")] }), { status: 200 }))
    );
    const { container } = render(<ExportPanel run={run()} scenario={scenarioFixture} reduction={reduction} />);
    screen.getByRole("button", { name: "Export JSON" }).click();
    await screen.findByRole("region", { name: "Export JSON preview" });
    expect(await violations(container)).toEqual([]);
  });
});
