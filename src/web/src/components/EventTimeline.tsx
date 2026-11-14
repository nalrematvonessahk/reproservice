import { useMemo, useState } from "react";
import type { RunEvent } from "../types";

interface Props {
  events: RunEvent[];
}

function resultTone(ev: RunEvent): string {
  if (ev.result === "dropped") return "tone-original-failure";
  if (ev.result === "faulted") return "tone-other-failure";
  return "tone-pass";
}

export default function EventTimeline({ events }: Props) {
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const byId = useMemo(() => new Map(events.map((e) => [e.event_id, e])), [events]);
  const selected = selectedId ? byId.get(selectedId) ?? null : null;
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
      <div className="timeline">
        {events.map((ev) => (
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
                {parentOfSelected.status_code}) at {parentOfSelected.monotonic_offset_ms}ms
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
