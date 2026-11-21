import { useMemo, useState } from "react";
import type { RunEvent, Scenario } from "../types";
import { NO_FILTER, distinct, filterEvents } from "../events";
import type { EventFilter } from "../events";

interface Props {
  events: RunEvent[];
  // Used to resolve an event's action_id (an intervention id) into the intervention
  // that fired, so the detail panel can show the intervention result (FR10).
  scenario: Scenario | null;
}

function resultTone(ev: RunEvent): string {
  if (ev.result === "dropped") return "tone-original-failure";
  if (ev.result === "faulted") return "tone-other-failure";
  return "tone-pass";
}

export default function EventTimeline({ events, scenario }: Props) {
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [filter, setFilter] = useState<EventFilter>(NO_FILTER);
  const byId = useMemo(() => new Map(events.map((e) => [e.event_id, e])), [events]);
  const services = useMemo(() => distinct(events.flatMap((e) => [e.source, e.target])), [events]);
  const phases = useMemo(() => distinct(events.map((e) => e.phase)), [events]);
  const results = useMemo(() => distinct(events.map((e) => e.result)), [events]);
  const visible = useMemo(() => filterEvents(events, filter), [events, filter]);
  const selected = selectedId ? byId.get(selectedId) ?? null : null;
  const intervention = selected?.action_id
    ? scenario?.interventions.find((iv) => iv.id === selected.action_id) ?? null
    : null;
  const parentOfSelected = selected?.parent_event_id ? byId.get(selected.parent_event_id) ?? null : null;

  if (events.length === 0) {
    return (
      <div className="card">
        <h2>3. Event timeline</h2>
        <p className="muted">No events yet. Start a run to see the proxy's request/response/fault log here.</p>
      </div>
    );
  }

  return (
    <div className="card">
      <h2>3. Event timeline</h2>
      <p className="muted small">
        Click an event for its selector and intervention result. A "dropped" fault event points at the response
        event it swallowed (parent_event_id) -- that pair is the "lost a successful response" story.
      </p>
      <div className="row filter-row">
        <div>
          <label htmlFor="filter-service">Service</label>
          <select
            id="filter-service"
            value={filter.service}
            onChange={(e) => setFilter({ ...filter, service: e.target.value })}
          >
            <option value="">All services</option>
            {services.map((v) => (
              <option key={v} value={v}>
                {v}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label htmlFor="filter-phase">Event phase</label>
          <select id="filter-phase" value={filter.phase} onChange={(e) => setFilter({ ...filter, phase: e.target.value })}>
            <option value="">All phases</option>
            {phases.map((v) => (
              <option key={v} value={v}>
                {v}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label htmlFor="filter-result">Event result</label>
          <select
            id="filter-result"
            value={filter.result}
            onChange={(e) => setFilter({ ...filter, result: e.target.value })}
          >
            <option value="">All results</option>
            {results.map((v) => (
              <option key={v} value={v}>
                {v}
              </option>
            ))}
          </select>
        </div>
        <button className="secondary" onClick={() => setFilter(NO_FILTER)} disabled={visible.length === events.length}>
          Clear filters
        </button>
      </div>
      <p className="muted small" aria-live="polite">
        Showing {visible.length} of {events.length} event(s).
      </p>
      {visible.length === 0 && <p className="muted">No events match the selected filters.</p>}
      <div className="timeline">
        {visible.map((ev) => (
          <button
            key={ev.event_id}
            className={`event-row ${selectedId === ev.event_id ? "event-row-selected" : ""}`}
            onClick={() => setSelectedId(ev.event_id)}
          >
            <span className="time">{ev.monotonic_offset_ms}ms</span>
            <span className={`badge badge-sm ${resultTone(ev)}`}>{ev.phase}/{ev.result}</span>
            <span className="event-desc">
              {ev.source} &rarr; {ev.target} &middot; {ev.method} {ev.normalized_path} &middot; attempt {ev.attempt}
              {ev.status_code ? ` · ${ev.status_code}` : ""}
              {ev.action_id && <span className="muted"> &middot; action {ev.action_id}</span>}
            </span>
          </button>
        ))}
      </div>

      {selected && (
        <div className="detail-panel">
          <h3>Event detail</h3>
          <dl className="kv">
            <dt>event_id</dt>
            <dd>
              <code>{selected.event_id}</code>
            </dd>
            <dt>logical_operation</dt>
            <dd>{selected.logical_operation}</dd>
            <dt>selector</dt>
            <dd>
              {selected.source} &rarr; {selected.target} &middot; {selected.method} {selected.normalized_path}{" "}
              (attempt {selected.attempt})
            </dd>
            <dt>phase / result</dt>
            <dd>
              {selected.phase} / {selected.result}
              {selected.status_code ? ` (status ${selected.status_code})` : ""}
            </dd>
            {selected.action_id && (
              <>
                <dt>action_id</dt>
                <dd>
                  <code>{selected.action_id}</code>
                </dd>
              </>
            )}
            {selected.action_id && (
              <>
                <dt>intervention</dt>
                <dd>
                  {intervention ? (
                    <>
                      {intervention.action.type}
                      {intervention.action.delay_ms ? ` ${intervention.action.delay_ms}ms` : ""}
                      {intervention.action.status_code ? ` status ${intervention.action.status_code}` : ""} &mdash;
                      selector {intervention.selector.logical_operation} / {intervention.selector.source} &rarr;{" "}
                      {intervention.selector.target} / {intervention.selector.method}{" "}
                      {intervention.selector.normalized_path} / attempt {intervention.selector.attempt}
                    </>
                  ) : (
                    <span className="muted">not defined in the loaded scenario</span>
                  )}
                </dd>
              </>
            )}
            <dt>monotonic_offset_ms</dt>
            <dd>{selected.monotonic_offset_ms}</dd>
            <dt>created_at</dt>
            <dd>{selected.created_at}</dd>
          </dl>
          {parentOfSelected && (
            <div className="parent-link">
              <strong>Parent event (the response this fault acted on):</strong>
              <p>
                {parentOfSelected.phase}/{parentOfSelected.result} for {parentOfSelected.source} &rarr;{" "}
                {parentOfSelected.target} {parentOfSelected.method} {parentOfSelected.normalized_path} (status{" "}
                {parentOfSelected.status_code ?? "n/a"}) at {parentOfSelected.monotonic_offset_ms}ms
              </p>
              <p className="muted small">
                This response was forwarded successfully, then the fault dropped it before the caller saw it --
                hence the caller's retry you see later in the timeline.
              </p>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
