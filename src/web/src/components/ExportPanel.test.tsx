// @vitest-environment jsdom
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import ExportPanel from "./ExportPanel";
import { ev, run, scenarioFixture } from "../fixtures";
import type { ReduceResult } from "../types";

const reduction: ReduceResult = {
  reduction_id: "r1",
  algorithm: "ddmin",
  accepted_intervention_ids: ["drop-payments-response"],
  runs_used: 5,
  stop_reason: "minimal",
  unstable: false,
};

function mockFetch(status: number, body: unknown) {
  const fn = vi.fn(async (_input: RequestInfo | URL) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } }));
  vi.stubGlobal("fetch", fn);
  return fn;
}

beforeEach(() => {
  vi.useRealTimers();
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("<ExportPanel/>", () => {
  it("exports the run with the scenario configuration", async () => {
    const fetchFn = mockFetch(200, { schema_version: 1, run: run(), events: [ev("e1"), ev("e2")] });
    render(<ExportPanel run={run()} scenario={scenarioFixture} reduction={null} />);
    expect(screen.getByRole("button", { name: "Download file" })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Export JSON" }));
    const preview = await screen.findByRole("region", { name: "Export JSON preview" });
    expect(preview).toHaveAttribute("tabindex", "0");
    expect(preview).toHaveTextContent('"id": "double-payment-demo"');
    expect(screen.getByText(/2 event\(s\)/)).toBeInTheDocument();
    expect(String(fetchFn.mock.calls[0][0])).toContain("/api/runs/run-1/export");
    expect(screen.getByRole("button", { name: "Download file" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Refresh export" })).toBeInTheDocument();
  });

  it("includes the reduction result when one was made", async () => {
    mockFetch(200, { schema_version: 1, run: run(), events: [ev("e1")] });
    render(<ExportPanel run={run()} scenario={scenarioFixture} reduction={reduction} />);
    await userEvent.click(screen.getByRole("button", { name: "Export JSON" }));
    const preview = await screen.findByRole("region", { name: "Export JSON preview" });
    expect(preview).toHaveTextContent('"reduction_id": "r1"');
    expect(screen.getByText(/includes reduction/)).toBeInTheDocument();
  });

  it("notes an export without events", async () => {
    mockFetch(200, { schema_version: 1, run: run(), events: null });
    render(<ExportPanel run={run()} scenario={scenarioFixture} reduction={null} />);
    await userEvent.click(screen.getByRole("button", { name: "Export JSON" }));
    expect(await screen.findByText(/this run produced no events/)).toBeInTheDocument();
  });

  it("warns when the event log may be truncated", async () => {
    const many = Array.from({ length: 10000 }, (_, i) => ev(`e${i}`));
    mockFetch(200, { schema_version: 1, run: run(), events: many });
    render(<ExportPanel run={run()} scenario={scenarioFixture} reduction={null} />);
    await userEvent.click(screen.getByRole("button", { name: "Export JSON" }));
    expect(await screen.findByText("Event log may be truncated")).toBeInTheDocument();
  }, 20000);

  it("announces a failed export as an alert and offers no download", async () => {
    mockFetch(500, { code: "INTERNAL", message: "boom", correlation_id: "c-9" });
    render(<ExportPanel run={run()} scenario={scenarioFixture} reduction={null} />);
    await userEvent.click(screen.getByRole("button", { name: "Export JSON" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("boom");
    expect(screen.getByRole("button", { name: "Download file" })).toBeDisabled();
  });

  it("downloads the document under a run-specific file name", async () => {
    mockFetch(200, { schema_version: 1, run: run(), events: [ev("e1")] });
    const createUrl = vi.fn(() => "blob:test");
    const revokeUrl = vi.fn();
    Object.assign(URL, { createObjectURL: createUrl, revokeObjectURL: revokeUrl });
    const clicked: string[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      clicked.push(this.download);
    });
    render(<ExportPanel run={run()} scenario={scenarioFixture} reduction={null} />);
    await userEvent.click(screen.getByRole("button", { name: "Export JSON" }));
    await screen.findByRole("region", { name: "Export JSON preview" });
    await userEvent.click(screen.getByRole("button", { name: "Download file" }));
    await waitFor(() => expect(clicked).toEqual(["reproservice-run-1.json"]));
    expect(createUrl).toHaveBeenCalledTimes(1);
  });
});
