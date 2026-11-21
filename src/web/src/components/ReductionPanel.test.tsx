// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import ReductionPanel from "./ReductionPanel";
import { run } from "../fixtures";
import type { ReduceResult } from "../types";

const result: ReduceResult = {
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

function setup(over: Partial<Parameters<typeof ReductionPanel>[0]> = {}) {
  const props = {
    run: run({ outcome: "ORIGINAL_FAILURE" }),
    onReduce: vi.fn(),
    onCancel: vi.fn(),
    busy: false,
    result: null,
    error: null,
    ...over,
  };
  render(<ReductionPanel {...props} />);
  return props;
}

describe("<ReductionPanel/>", () => {
  it("is disabled until the run has a verdict", () => {
    setup({ run: run({ state: "RUNNING" }) });
    expect(screen.getByRole("button", { name: "Reduce" })).toBeDisabled();
    expect(screen.getByText(/must reach COMPLETED/)).toBeInTheDocument();
  });

  it("reduces with ddmin by default", async () => {
    const props = setup();
    await userEvent.click(screen.getByRole("button", { name: "Reduce" }));
    expect(props.onReduce).toHaveBeenCalledWith("ddmin");
  });

  it("reduces with the chosen algorithm", async () => {
    const props = setup();
    await userEvent.selectOptions(screen.getByLabelText("Algorithm"), "dependency_aware");
    await userEvent.click(screen.getByRole("button", { name: "Reduce" }));
    expect(props.onReduce).toHaveBeenCalledWith("dependency_aware");
  });

  it("offers cancel only while a reduction is running and announces progress", async () => {
    const props = setup({ busy: true });
    expect(screen.getByRole("button", { name: "Reducing..." })).toBeDisabled();
    expect(screen.getByRole("status")).toHaveTextContent(/full reset \+ run/);
    await userEvent.click(screen.getByRole("button", { name: "Cancel reduction" }));
    expect(props.onCancel).toHaveBeenCalledTimes(1);
  });

  it("keeps cancel disabled when idle", () => {
    setup();
    expect(screen.getByRole("button", { name: "Cancel reduction" })).toBeDisabled();
  });

  it("shows the accepted interventions, budget and 1-minimal flag", () => {
    setup({ result });
    expect(screen.getByText("stop_reason: minimal")).toBeInTheDocument();
    expect(screen.getByText("drop-payments-response")).toBeInTheDocument();
    expect(screen.getByText(/7 of 100 runs \(3 confirmation\)/)).toBeInTheDocument();
    expect(screen.getByText(/yes \(not a proof of global minimum\)/)).toBeInTheDocument();
  });

  it("says so when the full set was not confirmed", () => {
    setup({ result: { ...result, confirmed: false, one_minimal: false, stop_reason: "not_confirmed" } });
    expect(screen.getByText(/accepted_intervention_ids \(input set, NOT confirmed\)/)).toBeInTheDocument();
  });

  it("shows (none) when the fault is not needed", () => {
    setup({ result: { ...result, accepted_intervention_ids: [] } });
    expect(screen.getByText("(none)")).toBeInTheDocument();
  });

  it("announces a failure as an alert", () => {
    setup({ error: { code: "STAND_BUSY", message: "stand is busy", correlation_id: "c1" } });
    expect(screen.getByRole("alert")).toHaveTextContent("STAND_BUSY");
  });
});
