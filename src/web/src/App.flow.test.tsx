// @vitest-environment jsdom
// Drives the real <App/> through the main workflow (scenario -> run -> timeline -> reduce ->
// compare -> export) against an in-memory fake of the controller whose JSON shapes were
// checked against the live controller (see api.live.test.ts). It catches wiring, state and
// rendering bugs; it does not replace a look at the page in a real browser.
import { act } from "react-dom/test-utils";
import { createRoot, Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { ev } from "./fixtures";
import type { RunEvent } from "./types";

(globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

interface FakeRun {
  run_id: string;
  key: string;
  active: string[];
  polls: number;
}

function startFakeController(opts: { eventsNull?: boolean; reduceDelayMs?: number; exportStatus?: number } = {}) {
  const runs: FakeRun[] = [];
  const calls: string[] = [];
  const events: RunEvent[] = [
    ev("e1", { source: "orders", target: "payments", phase: "response", result: "forwarded", status_code: 200, created_at: "2026-11-21T10:00:00.100100Z" }),
    ev("e2", { source: "orders", target: "payments", phase: "fault", result: "dropped", action_id: "fault-1", parent_event_id: "e1", created_at: "2026-11-21T10:00:00.100200Z" }),
    ev("e3", { source: "orders", target: "inventory", phase: "response", result: "forwarded", status_code: 200, created_at: "2026-11-21T10:00:00.300000Z" }),
  ];
  const wire = (r: FakeRun) => ({
    run_id: r.run_id, scenario_id: "multi-fault-demo", version_id: "ver-1", idempotency_key: r.key,
    state: r.polls < 2 ? "RUNNING" : "COMPLETED",
    ...(r.polls < 2 ? {} : { outcome: r.active.includes("fault-1") ? "ORIGINAL_FAILURE" : "PASS" }),
    active_intervention_ids: r.active, created_at: "t", updated_at: "t",
  });
  const j = (body: unknown, status = 200) =>
    new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input));
    const path = url.pathname;
    const method = init?.method ?? "GET";
    calls.push(`${method} ${path}${url.search}`);
    if (method === "POST" && path === "/api/scenarios") return j({ schema_version: 1, version_id: "ver-1", scenario_id: "multi-fault-demo" });
    if (method === "POST" && path === "/api/runs") {
      const body = JSON.parse(String(init?.body));
      const existing = runs.find((r) => r.key === body.idempotency_key);
      if (existing) return j(wire(existing));
      const r: FakeRun = { run_id: `run-${runs.length + 1}`, key: body.idempotency_key, active: body.active_intervention_ids ?? ["fault-1", "fault-2-decoy"], polls: 0 };
      runs.push(r);
      return j({ run_id: r.run_id }, 202);
    }
    const m = path.match(/^\/api\/runs\/([^/]+)(\/.*)?$/);
    if (m) {
      const r = runs.find((x) => x.run_id === m[1]);
      if (!r) return j({ code: "RUN_NOT_FOUND", message: "run not found", correlation_id: "corr-x" }, 404);
      if (!m[2]) { r.polls++; return j(wire(r)); }
      if (m[2] === "/events") {
        if (r.polls < 2 || opts.eventsNull) return j({ events: null, cursor: "" });
        // like the real controller: the cursor is millisecond-truncated, so the last
        // event of the previous page comes back again
        const after = url.searchParams.get("cursor");
        const list = after ? events.slice(1) : events;
        return j({ events: list, cursor: "1795281068114" });
      }
      if (m[2] === "/reduce") {
        await new Promise((res) => setTimeout(res, opts.reduceDelayMs ?? 0));
        return j({ reduction_id: "red-1", run_id: r.run_id, algorithm: "ddmin", state: "COMPLETED", accepted_intervention_ids: ["fault-1"], runs_used: 7, max_runs: 100, stop_reason: "minimal", unstable: false, confirmed: true, one_minimal: true, confirmation_runs: 6, cache_hits: 0, skipped_invalid: 0, duration_ms: 10 });
      }
      if (m[2] === "/cancel") return j({ status: "cancel_requested" });
      if (m[2] === "/export") {
        if (opts.exportStatus) return j({ code: "DB_ERROR", message: "db down", correlation_id: "corr-9" }, opts.exportStatus);
        return j({ schema_version: 1, run: wire(r), events: opts.eventsNull ? null : events });
      }
    }
    return j({ code: "NOT_FOUND", message: path, correlation_id: "" }, 404);
  });
  vi.stubGlobal("fetch", fetchMock);
  return { calls, runs };
}

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});
afterEach(() => {
  act(() => root.unmount());
  container.remove();
  vi.unstubAllGlobals();
});

const text = () => container.textContent ?? "";
const button = (label: RegExp | string) =>
  Array.from(container.querySelectorAll("button")).find((b) =>
    typeof label === "string" ? b.textContent === label : label.test(b.textContent ?? "")
  ) as HTMLButtonElement | undefined;

async function click(label: RegExp | string) {
  const b = button(label);
  if (!b) throw new Error(`button not found: ${label}\n${text().slice(0, 400)}`);
  if (b.disabled) throw new Error(`button disabled: ${label}`);
  await act(async () => { b.click(); });
}
// React tracks the value property itself, so set it through the element's prototype.
function setNativeValue(el: HTMLElement, value: string) {
  Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el), "value")!.set!.call(el, value);
}
async function choose(id: string, value: string) {
  const el = container.querySelector(`#${id}`) as HTMLSelectElement;
  await act(async () => {
    setNativeValue(el, value);
    el.dispatchEvent(new Event("change", { bubbles: true }));
  });
}
async function typeInto(el: HTMLTextAreaElement, value: string) {
  await act(async () => {
    setNativeValue(el, value);
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
async function waitFor(cond: () => boolean, what: string, ms = 8000) {
  const t0 = Date.now();
  while (!cond()) {
    if (Date.now() - t0 > ms) throw new Error(`timed out waiting for: ${what}\n${text().slice(0, 600)}`);
    await act(async () => { await new Promise((r) => setTimeout(r, 25)); });
  }
}
async function loadMultiFaultAndRun() {
  await act(async () => { root.render(<App />); });
  await choose("example-select", "multi-fault");
  await click(/Validate & load scenario/);
  await waitFor(() => !!button("Start run"), "Start run button");
  await click("Start run");
  await waitFor(
    () => container.querySelector(".status-block .badge")?.textContent === "ORIGINAL_FAILURE",
    "run status badge to show ORIGINAL_FAILURE"
  );
}

describe("main workflow", () => {
  it("starts empty with nothing runnable", async () => {
    startFakeController();
    await act(async () => { root.render(<App />); });
    expect(text()).toContain("No scenario loaded");
    expect(button("Start run")).toBeUndefined();
    expect(text()).not.toContain("Export JSON");
  });

  it("shows invalid JSON and non-object input without calling the controller", async () => {
    const { calls } = startFakeController();
    await act(async () => { root.render(<App />); });
    const ta = container.querySelector("#scenario-text") as HTMLTextAreaElement;
    for (const [bad, msg] of [["{nope", /INVALID_JSON/], ["[]", /must be a JSON object/], ["null", /must be a JSON object/]] as const) {
      await typeInto(ta, bad);
      await click(/Validate & load scenario/);
      expect(container.querySelector('[role="alert"]')?.textContent).toMatch(msg);
    }
    expect(calls).toEqual([]);
  });

  it("does not crash on a scenario that omits depends_on", async () => {
    startFakeController();
    await act(async () => { root.render(<App />); });
    const ta = container.querySelector("#scenario-text") as HTMLTextAreaElement;
    const sc = JSON.parse(ta.value);
    for (const iv of sc.interventions) delete iv.depends_on;
    await typeInto(ta, JSON.stringify(sc));
    await click(/Validate & load scenario/);
    await waitFor(() => !!button("Start run"), "Start run button");
    expect(text()).toContain("fault-1");
  });

  it("runs, lists each event once, filters the timeline and resolves the intervention", async () => {
    startFakeController();
    await loadMultiFaultAndRun();
    await waitFor(() => container.querySelectorAll("button.event-row").length === 3, "3 timeline rows (no duplicates)");
    expect(text()).toContain("Showing 3 of 3 event(s)");

    await choose("filter-service", "inventory");
    expect(container.querySelectorAll("button.event-row").length).toBe(1);
    await choose("filter-service", "");
    await choose("filter-phase", "fault");
    expect(container.querySelectorAll("button.event-row").length).toBe(1);
    await choose("filter-phase", "");
    await choose("filter-result", "forwarded");
    await choose("filter-service", "inventory");
    await choose("filter-phase", "fault");
    expect(text()).toContain("No events match the selected filters.");
    await click("Clear filters");
    expect(container.querySelectorAll("button.event-row").length).toBe(3);

    const faultRow = Array.from(container.querySelectorAll("button.event-row")).find((r) => r.textContent?.includes("fault/dropped")) as HTMLButtonElement;
    await act(async () => { faultRow.click(); });
    expect(text()).toContain("Event detail");
    expect(text()).toContain("drop_response_after_success");
    expect(text()).toContain("Parent event");
  });

  it("shows an empty timeline when the controller returns events: null", async () => {
    startFakeController({ eventsNull: true });
    await loadMultiFaultAndRun();
    expect(text()).toContain("No events yet");
  });

  it("reduces, offers cancellation while it runs, and reports the result", async () => {
    startFakeController({ reduceDelayMs: 150 });
    await loadMultiFaultAndRun();
    expect(button("Cancel reduction")?.disabled).toBe(true);
    await click("Reduce"); // returns once the click is handled; the reduce request stays pending
    await waitFor(() => button("Cancel reduction")?.disabled === false, "Cancel reduction enabled while reducing");
    expect(button("Start a different run")?.disabled).toBe(true);
    await waitFor(() => text().includes("stop_reason: minimal"), "reduction result");
    expect(text()).toContain("7 of 100 runs");
    expect(text()).toContain("1-minimal");
    expect(button("Cancel reduction")?.disabled).toBe(true);
  });

  it("explains an idempotent replay instead of silently showing the old run", async () => {
    const { runs } = startFakeController();
    await loadMultiFaultAndRun();
    // narrow the selection and press Start again with the same key
    const boxes = container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]');
    await act(async () => { boxes[1].click(); });
    await click("Start run");
    await waitFor(() => text().includes("already used"), "replay notice");
    expect(text()).toContain("not the 1 selected now");
    expect(runs).toHaveLength(1);
  });

  it("compares with repeats and never says 'fixed'", async () => {
    const { runs } = startFakeController();
    await loadMultiFaultAndRun();
    await choose("compare-mode", "none");
    await choose("compare-repeats", "3");
    await click("Run comparison");
    await waitFor(() => text().includes("Result of the candidate set"), "comparison summary", 15000);
    expect(runs).toHaveLength(4);
    expect(runs.slice(1).every((r) => r.active.length === 0)).toBe(true);
    expect(text()).toContain("PASS 3/3");
    expect(text()).toContain("not proof");
    expect(text()).toContain("none pinned");
    expect(text().toLowerCase()).not.toMatch(/\bfixed\b(?!\s*\/)/);
  }, 25000);

  it("a single comparison run draws no conclusion", async () => {
    startFakeController();
    await loadMultiFaultAndRun();
    await choose("compare-repeats", "1");
    await click("Run comparison");
    await waitFor(() => text().includes("not enough to conclude"), "insufficient-repeats message", 10000);
  }, 15000);

  it("exports scenario configuration, outcome and events", async () => {
    startFakeController();
    await loadMultiFaultAndRun();
    await click("Reduce");
    await waitFor(() => text().includes("stop_reason: minimal"), "reduction");
    await click("Export JSON");
    await waitFor(() => !!container.querySelector("pre.export-pre"), "export preview");
    const doc = JSON.parse(container.querySelector("pre.export-pre")!.textContent!);
    expect(doc.schema_version).toBe(1);
    expect(doc.scenario.id).toBe("multi-fault-demo");
    expect(doc.run.outcome).toBe("ORIGINAL_FAILURE");
    expect(doc.events).toHaveLength(3);
    expect(doc.reduction.accepted_intervention_ids).toEqual(["fault-1"]);
    expect(button("Download file")?.disabled).toBe(false);
  });

  it("exports a run without events as an empty list and says so", async () => {
    startFakeController({ eventsNull: true });
    await loadMultiFaultAndRun();
    await click("Export JSON");
    await waitFor(() => !!container.querySelector("pre.export-pre"), "export preview");
    expect(JSON.parse(container.querySelector("pre.export-pre")!.textContent!).events).toEqual([]);
    expect(text()).toContain("this run produced no events");
  });

  it("shows an export error with its correlation id and keeps download disabled", async () => {
    startFakeController({ exportStatus: 500 });
    await loadMultiFaultAndRun();
    await click("Export JSON");
    await waitFor(() => text().includes("DB_ERROR"), "export error");
    expect(text()).toContain("corr-9");
    expect(button("Download file")?.disabled).toBe(true);
  });

  it("Reset clears everything", async () => {
    startFakeController();
    await loadMultiFaultAndRun();
    await click("Reset");
    expect(text()).toContain("No scenario loaded");
    expect(button("Export JSON")).toBeUndefined();
  });
});
