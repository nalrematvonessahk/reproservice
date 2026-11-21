import { describe, expect, it } from "vitest";
import { NO_FILTER, distinct, filterEvents, mergeEvents } from "./events";
import { ev } from "./fixtures";

describe("mergeEvents", () => {
  it("drops the last event the controller returns again on the next poll", () => {
    const first = [ev("a"), ev("b")];
    const second = [ev("b"), ev("c")]; // ms-truncated cursor re-sends b
    const merged = mergeEvents(first, second);
    expect(merged.map((e) => e.event_id)).toEqual(["a", "b", "c"]);
  });

  it("returns the same array when nothing is new", () => {
    const first = [ev("a")];
    expect(mergeEvents(first, [ev("a")])).toBe(first);
    expect(mergeEvents(first, [])).toBe(first);
  });

  it("dedupes within one page", () => {
    expect(mergeEvents([], [ev("a"), ev("a")]).length).toBe(1);
  });
});

describe("filterEvents", () => {
  const all = [
    ev("1", { source: "orders", target: "payments", phase: "response", result: "forwarded" }),
    ev("2", { source: "orders", target: "payments", phase: "fault", result: "dropped" }),
    ev("3", { source: "orders", target: "inventory", phase: "response", result: "forwarded" }),
  ];

  it("returns everything with no filter", () => {
    expect(filterEvents(all, NO_FILTER)).toHaveLength(3);
  });
  it("filters by service on either side of the call", () => {
    expect(filterEvents(all, { ...NO_FILTER, service: "inventory" }).map((e) => e.event_id)).toEqual(["3"]);
    expect(filterEvents(all, { ...NO_FILTER, service: "orders" })).toHaveLength(3);
  });
  it("filters by event type (phase and result) and combines filters", () => {
    expect(filterEvents(all, { ...NO_FILTER, phase: "fault" }).map((e) => e.event_id)).toEqual(["2"]);
    expect(filterEvents(all, { ...NO_FILTER, result: "forwarded", service: "payments" }).map((e) => e.event_id)).toEqual(["1"]);
    expect(filterEvents(all, { service: "inventory", phase: "fault", result: "" })).toEqual([]);
  });
  it("distinct sorts and dedupes", () => {
    expect(distinct(["b", "a", "b"])).toEqual(["a", "b"]);
  });
});
