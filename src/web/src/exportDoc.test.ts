import { describe, expect, it } from "vitest";
import { EXPORT_EVENT_LIMIT, buildExportDocument, exportFilename, mayBeTruncated, serializeExport } from "./exportDoc";
import { ev, run } from "./fixtures";
import { normalizeScenario } from "./scenario";
import example from "./examples/scenario.example.json";

const scenario = normalizeScenario(example);
const payload = { schema_version: 1, run: run({ outcome: "ORIGINAL_FAILURE" }), events: [ev("a"), ev("b", { parent_event_id: "a" })] };

describe("export document (FR12)", () => {
  it("contains schema_version, configuration, outcome and the event list", () => {
    const parsed = JSON.parse(serializeExport(buildExportDocument(payload, scenario, null)));
    expect(parsed.schema_version).toBe(1);
    expect(parsed.scenario.id).toBe(scenario.id);
    expect(parsed.scenario.interventions).toHaveLength(scenario.interventions.length);
    expect(parsed.run.outcome).toBe("ORIGINAL_FAILURE");
    expect(parsed.events.map((e: { event_id: string }) => e.event_id)).toEqual(["a", "b"]);
    expect(parsed.events[1].parent_event_id).toBe("a");
    expect(parsed.reduction).toBeUndefined();
  });

  it("includes the reduction only when one exists", () => {
    const red = { reduction_id: "red-1", algorithm: "ddmin" as const, accepted_intervention_ids: ["fault-1"], runs_used: 5, stop_reason: "minimal", unstable: false };
    expect(buildExportDocument(payload, scenario, red).reduction).toEqual(red);
  });

  it("keeps an empty event list as [] (a run with no events is exported, not lost)", () => {
    const parsed = JSON.parse(serializeExport(buildExportDocument({ ...payload, events: [] }, scenario, null)));
    expect(parsed.events).toEqual([]);
  });

  it("carries no secret header fields", () => {
    const text = serializeExport(buildExportDocument(payload, scenario, null)).toLowerCase();
    for (const name of ["authorization", "cookie", "set-cookie", "x-api-key"]) expect(text).not.toContain(name);
  });

  it("is lossless: every event field survives serialisation", () => {
    const parsed = JSON.parse(serializeExport(buildExportDocument(payload, scenario, null)));
    expect(parsed.events).toEqual(payload.events);
    expect(parsed.run).toEqual(payload.run);
  });

  it("builds a safe filename", () => {
    expect(exportFilename(run({ run_id: "run-17-2" }))).toBe("reproservice-run-17-2.json");
    expect(exportFilename(run({ run_id: "../x/y z" }))).toBe("reproservice-.._x_y_z.json");
  });

  it("flags a log that hit the controller's event cap", () => {
    expect(mayBeTruncated(payload)).toBe(false);
    const big = { ...payload, events: Array.from({ length: EXPORT_EVENT_LIMIT }, (_, i) => ev(`e${i}`)) };
    expect(mayBeTruncated(big)).toBe(true);
  });
});
