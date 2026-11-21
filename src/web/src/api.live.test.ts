// Contract test against a REAL controller + stand (docs/07-architecture.md "Черновой API").
// Skipped unless VITE_LIVE=1. Run:
//   VITE_LIVE=1 VITE_API_BASE=http://127.0.0.1:7000 npm test -- api.live
// It starts real runs and a real reduction, so it needs the Docker stand and takes ~1 minute.
import { describe, expect, it } from "vitest";
import {
  ApiError, cancelRun, createRun, createScenario, exportRun, getRun, isExistingRun, listEvents, reduceRun, waitForRun,
} from "./api";
import { mergeEvents } from "./events";
import { buildExportDocument, serializeExport } from "./exportDoc";
import { normalizeScenario } from "./scenario";
import multiFault from "./examples/multi-fault-demo.json";
import type { RunEvent } from "./types";

const live = (import.meta as unknown as { env?: Record<string, string> }).env?.VITE_LIVE === "1";
const uniq = () => `live-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;

describe.skipIf(!live)("web API client vs live controller", () => {
  it("covers the main workflow end to end", async () => {
    // 1. scenario -> version
    const created = await createScenario(multiFault);
    expect(created.schema_version).toBe(1);
    expect(created.version_id).toMatch(/^ver-/);
    const scenario = normalizeScenario(multiFault);

    // 2. run with every intervention
    const key = uniq();
    const ids = scenario.interventions.map((i) => i.id);
    const accepted = await createRun({ version_id: created.version_id, idempotency_key: key, active_intervention_ids: ids });
    expect(isExistingRun(accepted)).toBe(false);
    const done = await waitForRun(accepted.run_id, { intervalMs: 200 });
    expect(done.state).toBe("COMPLETED");
    expect(done.outcome).toBe("ORIGINAL_FAILURE");
    expect(done.active_intervention_ids).toEqual(ids);

    // idempotent replay returns the existing run (200 + full Run), not a second run
    const replay = await createRun({ version_id: created.version_id, idempotency_key: key, active_intervention_ids: ids });
    expect(isExistingRun(replay)).toBe(true);
    expect(replay.run_id).toBe(accepted.run_id);

    // 3. events: incremental polling like the UI does, then compare with the export
    let merged: RunEvent[] = [];
    let cursor: string | undefined;
    for (let i = 0; i < 3; i++) {
      const page = await listEvents(accepted.run_id, cursor);
      if (page.events.length) cursor = page.cursor;
      merged = mergeEvents(merged, page.events);
    }
    expect(merged.length).toBeGreaterThan(0);
    expect(new Set(merged.map((e) => e.event_id)).size).toBe(merged.length);
    const dropped = merged.find((e) => e.result === "dropped");
    expect(dropped?.action_id).toBeTruthy();
    expect(scenario.interventions.some((iv) => iv.id === dropped?.action_id)).toBe(true);

    // 4. export matches the events API and carries no secret header fields
    const exp = await exportRun(accepted.run_id);
    expect(exp.schema_version).toBe(1);
    expect(exp.run.run_id).toBe(accepted.run_id);
    expect(exp.events.map((e) => e.event_id).sort()).toEqual(merged.map((e) => e.event_id).sort());
    const text = serializeExport(buildExportDocument(exp, scenario, null)).toLowerCase();
    for (const h of ["authorization", "cookie"]) expect(text).not.toContain(h);

    // cancel on a finished run changes nothing
    expect(await cancelRun(accepted.run_id)).toMatchObject({ status: "not_running", state: "COMPLETED" });
    expect((await getRun(accepted.run_id)).outcome).toBe("ORIGINAL_FAILURE");

    // 5. reduction
    const red = await reduceRun(accepted.run_id, "ddmin");
    expect(red.stop_reason).toBe("minimal");
    expect(red.accepted_intervention_ids.length).toBeGreaterThan(0);
    expect(red.accepted_intervention_ids.length).toBeLessThan(ids.length);
    expect(red.runs_used).toBeGreaterThan(0);
    expect(typeof red.unstable).toBe("boolean");
    expect(red.one_minimal).toBe(true);

    // 6. compare: an explicit empty list means zero active interventions (not "all")
    const baseline = await createRun({ version_id: created.version_id, idempotency_key: uniq(), active_intervention_ids: [] });
    const clean = await waitForRun(baseline.run_id, { intervalMs: 200 });
    expect(clean.active_intervention_ids).toEqual([]);
    expect(clean.outcome).toBe("PASS");
  }, 180_000);

  it("reports a run with no events as an empty list, and unknown ids as errors", async () => {
    expect((await listEvents("run-does-not-exist")).events).toEqual([]);
    for (const call of [() => getRun("run-does-not-exist"), () => exportRun("run-does-not-exist")]) {
      const err = await call().catch((e) => e);
      expect(err).toBeInstanceOf(ApiError);
      expect(err.status).toBe(404);
      expect(err.body.code).toBe("RUN_NOT_FOUND");
      expect(err.body.correlation_id).toBeTruthy();
    }
  });

  it("rejects bad input with the documented error envelope", async () => {
    const bad = await createScenario({ schema_version: 1 }).catch((e) => e);
    expect(bad).toBeInstanceOf(ApiError);
    expect(bad.status).toBe(422);
    expect(bad.body.code).toBe("INVALID_SCENARIO");

    const v = await createScenario(multiFault);
    const unknown = await createRun({ version_id: v.version_id, idempotency_key: uniq(), active_intervention_ids: ["nope"] }).catch((e) => e);
    expect(unknown.status).toBe(422);
    expect(unknown.body.code).toBe("UNKNOWN_INTERVENTION");

    const missing = await createRun({ version_id: "ver-missing", idempotency_key: uniq() }).catch((e) => e);
    expect(missing.status).toBe(404);
    expect(missing.body.code).toBe("VERSION_NOT_FOUND");
  });

  it("answers 409 STAND_BUSY to a second run while one is in flight", async () => {
    const v = await createScenario(multiFault);
    const first = await createRun({ version_id: v.version_id, idempotency_key: uniq() });
    const second = await createRun({ version_id: v.version_id, idempotency_key: uniq() }).catch((e) => e);
    expect(second).toBeInstanceOf(ApiError);
    expect(second.status).toBe(409);
    expect(second.body.code).toBe("STAND_BUSY");
    await waitForRun(first.run_id, { intervalMs: 200 });
  }, 60_000);
});
