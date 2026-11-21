import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, describeError, exportRun, getRun, isExistingRun, listEvents, parseErrorBody, reduceRun, waitForRun } from "./api";
import { run } from "./fixtures";

function mockFetch(...responses: Array<Response | Error>) {
  const fn = vi.fn();
  for (const r of responses) r instanceof Error ? fn.mockRejectedValueOnce(r) : fn.mockResolvedValueOnce(r);
  vi.stubGlobal("fetch", fn);
  return fn;
}
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("API client against the controller's real wire shapes", () => {
  it("listEvents turns the controller's `events: null` into []", async () => {
    mockFetch(json({ events: null, cursor: "" }));
    expect(await listEvents("run-1")).toEqual({ events: [], cursor: "" });
  });

  it("listEvents sends the cursor and escapes the run id", async () => {
    const f = mockFetch(json({ events: [], cursor: "" }));
    await listEvents("run/1", "123");
    expect(String(f.mock.calls[0][0])).toMatch(/\/api\/runs\/run%2F1\/events\?cursor=123$/);
  });

  it("exportRun normalises null events and null active ids", async () => {
    mockFetch(json({ schema_version: 1, run: { ...run(), active_intervention_ids: null }, events: null }));
    const res = await exportRun("run-1");
    expect(res.events).toEqual([]);
    expect(res.run.active_intervention_ids).toEqual([]);
  });

  it("exportRun surfaces the controller's error envelope", async () => {
    mockFetch(json({ code: "RUN_NOT_FOUND", message: "nope", correlation_id: "corr-1" }, 404));
    await expect(exportRun("x")).rejects.toMatchObject({ status: 404, body: { code: "RUN_NOT_FOUND", correlation_id: "corr-1" } });
  });

  it("reduceRun turns accepted_intervention_ids: null into []", async () => {
    mockFetch(json({ reduction_id: "r", algorithm: "ddmin", accepted_intervention_ids: null, runs_used: 1, stop_reason: "minimal", unstable: false }));
    expect((await reduceRun("run-1", "ddmin")).accepted_intervention_ids).toEqual([]);
  });

  it("reduceRun POSTs the algorithm in the body", async () => {
    const f = mockFetch(json({ reduction_id: "r", algorithm: "sequential", accepted_intervention_ids: [], runs_used: 1, stop_reason: "minimal", unstable: false }));
    await reduceRun("run-1", "sequential");
    const init = f.mock.calls[0][1] as RequestInit;
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({ algorithm: "sequential" });
  });

  it("reports an unreachable controller as NETWORK_ERROR with status 0", async () => {
    mockFetch(new TypeError("Failed to fetch"));
    const err = await getRun("r").catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(0);
    expect(err.body.code).toBe("NETWORK_ERROR");
  });

  it("keeps the text of a non-JSON error body (e.g. an HTML 502)", async () => {
    mockFetch(new Response("<h1>Bad Gateway</h1>", { status: 502 }));
    const err = await getRun("r").catch((e) => e);
    expect(err.body.code).toBe("HTTP_502");
    expect(err.body.message).toContain("Bad Gateway");
  });

  it("reports a 200 with a non-JSON body as INVALID_RESPONSE", async () => {
    mockFetch(new Response("ok", { status: 200 }));
    await expect(getRun("r")).rejects.toMatchObject({ body: { code: "INVALID_RESPONSE" } });
  });

  it("parseErrorBody fills a missing message and correlation id", () => {
    expect(parseErrorBody(500, "Server Error", JSON.stringify({ code: "DB_ERROR" }))).toEqual({
      code: "DB_ERROR",
      message: "Server Error",
      field: undefined,
      correlation_id: "",
    });
  });

  it("describeError handles non-API errors", () => {
    expect(describeError(new Error("boom")).message).toBe("boom");
    expect(describeError("x").code).toBe("UNKNOWN_ERROR");
  });

  it("isExistingRun tells the 200 replay from the 202 accept", () => {
    expect(isExistingRun(run())).toBe(true);
    expect(isExistingRun({ run_id: "r" })).toBe(false);
  });

  it("waitForRun polls until the run is terminal", async () => {
    mockFetch(json(run({ state: "RUNNING" })), json(run({ state: "ASSERTING" })), json(run({ state: "COMPLETED" })));
    const done = await waitForRun("run-1", { intervalMs: 1 });
    expect(done.state).toBe("COMPLETED");
  });

  it("waitForRun rides out transient failures but not persistent ones or 404s", async () => {
    mockFetch(new TypeError("down"), json({ code: "X", message: "m", correlation_id: "" }, 503), json(run({ state: "COMPLETED" })));
    expect((await waitForRun("run-1", { intervalMs: 1 })).state).toBe("COMPLETED");

    mockFetch(...Array.from({ length: 5 }, () => new TypeError("down")));
    await expect(waitForRun("run-1", { intervalMs: 1 })).rejects.toMatchObject({ body: { code: "NETWORK_ERROR" } });

    mockFetch(json({ code: "RUN_NOT_FOUND", message: "m", correlation_id: "" }, 404));
    await expect(waitForRun("run-1", { intervalMs: 1 })).rejects.toMatchObject({ status: 404 });
  });

  it("waitForRun stops when aborted", async () => {
    mockFetch(json(run({ state: "RUNNING" })));
    const ac = new AbortController();
    ac.abort();
    await expect(waitForRun("run-1", { signal: ac.signal, intervalMs: 1 })).rejects.toMatchObject({ name: "AbortError" });
  });
});
