import { useEffect, useRef, useState } from "react";
import { ApiError, getRun, listEvents } from "./api";
import type { Run, RunEvent } from "./types";

const TERMINAL = new Set(["COMPLETED", "CANCELLED", "INFRA_ERROR"]);
const POLL_MS = 350;

export interface ApiErrorLike {
  code: string;
  message: string;
  correlation_id: string;
}

// Polls GET /api/runs/{id} until the run reaches a terminal state. The demo stand
// finishes in roughly a second, but this deliberately does not assume synchronous
// completion -- it genuinely polls.
export function useRunPolling(runId: string | null) {
  const [run, setRun] = useState<Run | null>(null);
  const [error, setError] = useState<ApiErrorLike | null>(null);

  useEffect(() => {
    if (!runId) {
      setRun(null);
      return;
    }
    setRun(null);
    setError(null);
    const id = runId;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout>;

    async function tick() {
      try {
        const r = await getRun(id);
        if (cancelled) return;
        setRun(r);
        if (!TERMINAL.has(r.state)) {
          timer = setTimeout(tick, POLL_MS);
        }
      } catch (e) {
        if (cancelled) return;
        if (e instanceof ApiError) {
          setError({ code: e.body.code, message: e.body.message, correlation_id: e.body.correlation_id });
        } else {
          setError({ code: "UNKNOWN_ERROR", message: String(e), correlation_id: "" });
        }
      }
    }
    tick();
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [runId]);

  return { run, error };
}

// Polls GET /api/runs/{id}/events?cursor= incrementally while the run is active,
// and keeps the accumulated event list after the run terminates.
export function useEventPolling(runId: string | null, runState: string | null) {
  const [events, setEvents] = useState<RunEvent[]>([]);
  const cursorRef = useRef<string | undefined>(undefined);
  const runIdRef = useRef<string | null>(null);

  useEffect(() => {
    if (runIdRef.current !== runId) {
      runIdRef.current = runId;
      cursorRef.current = undefined;
      setEvents([]);
    }
  }, [runId]);

  useEffect(() => {
    if (!runId) return;
    const id = runId;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout>;
    const isTerminal = runState !== null && TERMINAL.has(runState);

    async function tick() {
      try {
        const res = await listEvents(id, cursorRef.current);
        if (cancelled) return;
        if (res.events.length > 0) {
          setEvents((prev) => [...prev, ...res.events]);
          cursorRef.current = res.cursor;
        }
      } catch {
        // event polling errors are non-fatal to the run view; just stop this cycle
      }
      if (!cancelled && !isTerminal) {
        timer = setTimeout(tick, POLL_MS);
      }
    }
    tick();
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
    // re-poll once more whenever runState flips (e.g. into terminal) to catch final events
  }, [runId, runState]);

  return events;
}
