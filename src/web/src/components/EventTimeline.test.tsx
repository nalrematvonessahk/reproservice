// @vitest-environment jsdom
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import EventTimeline from "./EventTimeline";
import { ev, scenarioFixture } from "../fixtures";

const events = [
  ev("e1", { phase: "request", monotonic_offset_ms: 1 }),
  ev("e2", { phase: "response", status_code: 200, monotonic_offset_ms: 5 }),
  ev("e3", { phase: "fault", result: "dropped", action_id: "drop-payments-response", parent_event_id: "e2", monotonic_offset_ms: 6 }),
  ev("e4", { target: "inventory", attempt: 2, phase: "response", status_code: 200, monotonic_offset_ms: 9 }),
];

describe("<EventTimeline/>", () => {
  it("shows an empty state without events", () => {
    render(<EventTimeline events={[]} scenario={scenarioFixture} />);
    expect(screen.getByText(/No events yet/)).toBeInTheDocument();
  });

  it("lists every event and the visible count", () => {
    render(<EventTimeline events={events} scenario={scenarioFixture} />);
    expect(screen.getByText("Showing 4 of 4 event(s).")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /fault\/dropped/ })).toBeInTheDocument();
  });

  it("filters by result and clears the filter", async () => {
    render(<EventTimeline events={events} scenario={scenarioFixture} />);
    await userEvent.selectOptions(screen.getByLabelText("Event result"), "dropped");
    expect(screen.getByText("Showing 1 of 4 event(s).")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Clear filters" }));
    expect(screen.getByText("Showing 4 of 4 event(s).")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Clear filters" })).toBeDisabled();
  });

  it("filters by service and tells when nothing matches", async () => {
    render(<EventTimeline events={events} scenario={scenarioFixture} />);
    await userEvent.selectOptions(screen.getByLabelText("Service"), "inventory");
    expect(screen.getByText("Showing 1 of 4 event(s).")).toBeInTheDocument();
    await userEvent.selectOptions(screen.getByLabelText("Event phase"), "fault");
    expect(screen.getByText("No events match the selected filters.")).toBeInTheDocument();
  });

  it("shows the intervention and the parent response of a dropped event", async () => {
    render(<EventTimeline events={events} scenario={scenarioFixture} />);
    await userEvent.click(screen.getByRole("button", { name: /fault\/dropped/ }));
    const detail = screen.getByRole("heading", { name: "Event detail" }).parentElement as HTMLElement;
    expect(within(detail).getByText("drop_response_after_success", { exact: false })).toBeInTheDocument();
    expect(within(detail).getByText(/Parent event/)).toBeInTheDocument();
    expect(within(detail).getByText(/status 200\) at 5ms/)).toBeInTheDocument();
  });

  it("says when the action is not part of the loaded scenario", async () => {
    render(<EventTimeline events={events} scenario={{ ...scenarioFixture, interventions: [] }} />);
    await userEvent.click(screen.getByRole("button", { name: /fault\/dropped/ }));
    expect(screen.getByText("not defined in the loaded scenario")).toBeInTheDocument();
  });

  it("lets the keyboard select an event", async () => {
    render(<EventTimeline events={events} scenario={scenarioFixture} />);
    const row = screen.getByRole("button", { name: /fault\/dropped/ });
    row.focus();
    await userEvent.keyboard("{Enter}");
    expect(screen.getByRole("heading", { name: "Event detail" })).toBeInTheDocument();
  });
});
