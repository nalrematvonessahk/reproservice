import type { RunEvent } from "./types";

// Appends `incoming` to `existing`, skipping event_ids already present, in arrival order.
// GET /events?cursor= is millisecond-granular on the wire but the stored timestamps are
// microsecond-granular, so the last event of the previous page is returned again on the
// next poll (docs/reviews/DEMO-54, finding 8). The UI must not show it twice.
export function mergeEvents(existing: RunEvent[], incoming: RunEvent[]): RunEvent[] {
  if (incoming.length === 0) return existing;
  const seen = new Set(existing.map((e) => e.event_id));
  const fresh: RunEvent[] = [];
  for (const e of incoming) {
    if (!seen.has(e.event_id)) {
      seen.add(e.event_id);
      fresh.push(e);
    }
  }
  return fresh.length === 0 ? existing : [...existing, ...fresh];
}

export interface EventFilter {
  service: string; // "" = all; matches source or target
  phase: string; // "" = all
  result: string; // "" = all
}

export const NO_FILTER: EventFilter = { service: "", phase: "", result: "" };

export function filterEvents(events: RunEvent[], f: EventFilter): RunEvent[] {
  return events.filter(
    (e) =>
      (!f.service || e.source === f.service || e.target === f.service) &&
      (!f.phase || e.phase === f.phase) &&
      (!f.result || e.result === f.result)
  );
}

export function distinct(values: string[]): string[] {
  return Array.from(new Set(values)).sort();
}
