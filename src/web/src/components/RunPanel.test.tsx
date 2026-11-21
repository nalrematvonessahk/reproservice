// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import RunPanel from "./RunPanel";
import { run, scenarioFixture } from "../fixtures";
import type { Run } from "../types";

function setup(over: Partial<Parameters<typeof RunPanel>[0]> = {}) {
  const props = {
    scenario: scenarioFixture,
    versionId: "ver-1",
    activeIds: new Set(scenarioFixture.interventions.map((iv) => iv.id)),
    onToggleIntervention: vi.fn(),
    idempotencyKey: "key-1",
    onIdempotencyKeyChange: vi.fn(),
    onStart: vi.fn(),
    onCancel: vi.fn(),
    starting: false,
    run: null as Run | null,
    runError: null,
    notice: null,
    ...over,
  };
  render(<RunPanel {...props} />);
  return props;
}

describe("<RunPanel/>", () => {
  it("lists every intervention as a labelled checkbox", () => {
    setup();
    const boxes = screen.getAllByRole("checkbox");
    expect(boxes).toHaveLength(2);
    expect(boxes[0]).toBeChecked();
    expect(screen.getByText("delay-inventory")).toBeInTheDocument();
    expect(screen.getByText(/depends_on: drop-payments-response/)).toBeInTheDocument();
  });

  it("reports a toggled intervention by id", async () => {
    const props = setup();
    await userEvent.click(screen.getAllByRole("checkbox")[1]);
    expect(props.onToggleIntervention).toHaveBeenCalledWith("delay-inventory");
  });

  it("starts a run and keeps cancel disabled while nothing is running", async () => {
    const props = setup();
    expect(screen.getByRole("button", { name: "Cancel run" })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Start run" }));
    expect(props.onStart).toHaveBeenCalledTimes(1);
  });

  it("disables Start when no intervention is selected", () => {
    setup({ activeIds: new Set() });
    expect(screen.getByRole("button", { name: "Start run" })).toBeDisabled();
  });

  it("locks Start and enables Cancel while a run is active", async () => {
    const props = setup({ run: run({ state: "RUNNING", outcome: "" }) });
    expect(screen.getByRole("button", { name: "Start run" })).toBeDisabled();
    const cancel = screen.getByRole("button", { name: "Cancel run" });
    expect(cancel).toBeEnabled();
    await userEvent.click(cancel);
    expect(props.onCancel).toHaveBeenCalledTimes(1);
    expect(screen.getAllByRole("checkbox")[0]).toBeDisabled();
  });

  it("shows the outcome and its explanation for a finished run", () => {
    setup({ run: run({ state: "COMPLETED", outcome: "ORIGINAL_FAILURE" }) });
    expect(screen.getByRole("status")).toHaveTextContent("ORIGINAL_FAILURE");
    expect(screen.getByText(/matching the scenario's declared invariant_id/)).toBeInTheDocument();
  });

  it("announces an API error as an alert with its correlation id", () => {
    setup({ runError: { code: "STAND_BUSY", message: "another run holds the stand", correlation_id: "c-42" } });
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("STAND_BUSY");
    expect(alert).toHaveTextContent("correlation_id: c-42");
  });

  it("shows an idempotent-replay notice as a status message", () => {
    setup({ notice: "This idempotency key was already used." });
    expect(screen.getByText("This idempotency key was already used.")).toBeInTheDocument();
  });

  it("toggles the raw run JSON as a keyboard-focusable labelled region", async () => {
    setup({ run: run() });
    await userEvent.click(screen.getByRole("button", { name: "Show raw run JSON" }));
    const region = screen.getByRole("region", { name: "Raw run JSON" });
    expect(region).toHaveAttribute("tabindex", "0");
    expect(region).toHaveTextContent('"run_id": "run-1"');
    await userEvent.click(screen.getByRole("button", { name: "Hide raw run JSON" }));
    expect(screen.queryByRole("region", { name: "Raw run JSON" })).not.toBeInTheDocument();
  });

  it("forwards edits of the idempotency key", async () => {
    const props = setup();
    await userEvent.type(screen.getByLabelText("Idempotency key"), "x");
    expect(props.onIdempotencyKeyChange).toHaveBeenCalledWith("key-1x");
  });
});
