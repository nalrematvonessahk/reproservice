import { useEffect, useRef, useState } from "react";
import { MAX_TRANSIENT_FAILURES, describeError, getRun, isTransientError, listEvents } from "./api";
import type { ErrorView } from "./api";
import { mergeEvents } from "./events";
import { isTerminalState } from "./types";
import type { Run, RunEvent } from "./types";

const POLL_MS = 350;
// The controller returns at most this many events per page (api.go listEvents).
const EVENT_PAGE = 500;
// After the error has been shown, keep polling slowly so the view recovers (and
// run-dependent controls unlock) once the controller is back.
const RECOVERY_POLL_MS = 3000;
// Retries of the final event drain after the run turned terminal.
const MAX_DRAIN_RETRIES = 5;

export type ApiErrorLike = ErrorView;

// Polls GET /api/runs/{id} until the run reaches a terminal state. A transient failure
// (controller restarting, network blip) is retried a few times instead of freezing the
// view on a stale non-terminal state; a 404 or a persistent failure is shown.
export function useRunPolling(runId: string | null) {
  const [run, setRun] = useState<Run | null>(null);
  const [error, setError] = useState<ApiErrorLike | null>(null);

  useEffect(() => {
    setRun(null);
    setError(null);
    if (!runId) return;
    const id = runId;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout>;
    let failures = 0;

    async function tick() {
      try {
        const r = await getRun(id);
        if (cancelled) return;
        failures = 0;
        setError(null);
        setRun(r);
        if (!isTerminalState(r.state)) timer = setTimeout(tick, POLL_MS);
      } catch (e) {
        if (cancelled) return;
        failures++;
        if (isTransientError(e) && failures < MAX_TRANSIENT_FAILURES) {
          timer = setTimeout(tick, POLL_MS * failures);
          return;
        }
        setError(describeError(e));
        // A 404 will not fix itself; a dead controller might.
        if (isTransientError(e)) timer = setTimeout(tick, RECOVERY_POLL_MS);
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

// Polls GET /api/runs/{id}/events?cursor= incrementally while the run is active, drains
// every page after it becomes terminal, and keeps the accumulated list afterwards.
export function useEventPolling(runId: string | null, runState: string | null) {
  const [events, setEvents] = useState<RunEvent[]>([]);
  const cursorRef = useRef<string | undefined>(undefined);
  const eventsRef = useRef<RunEvent[]>([]);
  const runIdRef = useRef<string | null>(null);

  useEffect(() => {
    if (runIdRef.current !== runId) {
      runIdRef.current = runId;
      cursorRef.current = undefined;
      eventsRef.current = [];
      setEvents([]);
    }
  }, [runId]);

  useEffect(() => {
    if (!runId) return;
    const id = runId;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout>;
    const isTerminal = isTerminalState(runState);

    async function drain() {
      // Keep fetching while a full page comes back, so a run with more than one page
      // of events is not silently cut off after it finished.
      for (;;) {
        const res = await listEvents(id, cursorRef.current);
        if (cancelled) return;
        const merged = mergeEvents(eventsRef.current, res.events);
        const grew = merged !== eventsRef.current;
        if (res.events.length > 0) cursorRef.current = res.cursor;
        if (grew) {
          eventsRef.current = merged;
          setEvents(merged);
        }
        // Stop on a short page, or when a full page brought nothing new (no progress).
        if (res.events.length < EVENT_PAGE || !grew) return;
      }
    }

    let drainFailures = 0;
    async function tick() {
      try {
        await drain();
        drainFailures = 0;
      } catch {
        // Event polling errors are non-fatal for the run view. While the run is active
        // the next tick retries; after it ended, retry a bounded number of times so the
        // final events are not lost to one failed request.
        drainFailures++;
        if (!cancelled && isTerminal && drainFailures < MAX_DRAIN_RETRIES) {
          timer = setTimeout(tick, POLL_MS * drainFailures);
          return;
        }
      }
      if (!cancelled && !isTerminal) timer = setTimeout(tick, POLL_MS);
    }
    tick();
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
    // Re-runs once when runState flips to terminal so the final events are fetched.
  }, [runId, runState]);

  return events;
}
