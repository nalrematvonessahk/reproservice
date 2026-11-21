import { useEffect, useRef, useState } from "react";
import { ApiError, cancelRun, createRun, describeError, waitForRun } from "../api";
import type { ErrorView } from "../api";
import type { Run, Scenario } from "../types";
import { MIN_REPEATS_FOR_CONCLUSION, summarizeRepeats } from "../compare";
import StatusBadge from "./StatusBadge";
import { explainRun } from "../explain";

interface Props {
  scenario: Scenario;
  versionId: string;
  baselineRun: Run;
  reducedIds: string[] | null;
  // Lets the page keep navigation locked while repeats are in flight.
  onBusyChange?: (busy: boolean) => void;
}

const REPEAT_OPTIONS = [1, MIN_REPEATS_FOR_CONCLUSION, 5];
// The controller releases the stand just after it writes COMPLETED (orchestrator.go
// runRegistered), so back-to-back repeats could in principle hit a 409 STAND_BUSY in that
// window. Retry that code, and only that one. (Not reproduced in manual testing.)
const BUSY_RETRIES = 10;
const BUSY_DELAY_MS = 300;

function sleep(ms: number) {
  return new Promise((r) => setTimeout(r, ms));
}

async function startRunWhenFree(body: Parameters<typeof createRun>[0]) {
  for (let i = 0; ; i++) {
    try {
      return await createRun(body);
    } catch (e) {
      if (!(e instanceof ApiError) || e.body.code !== "STAND_BUSY" || i >= BUSY_RETRIES) throw e;
      await sleep(BUSY_DELAY_MS);
    }
  }
}

function Environment({ scenario, versionId }: { scenario: Scenario; versionId: string }) {
  const digests = Object.entries(scenario.environment.image_digests ?? {});
  return (
    <dl className="kv compare-env">
      <dt>version_id</dt>
      <dd>
        <code>{versionId}</code>
      </dd>
      <dt>seed</dt>
      <dd>
        <code>{scenario.environment.seed_id}</code>
        {scenario.environment.seed_hash ? ` (hash ${scenario.environment.seed_hash})` : ""}
      </dd>
      <dt>image digests</dt>
      <dd>
        {digests.length === 0 ? (
          <span className="muted">none pinned in this scenario (the controller does not verify digests)</span>
        ) : (
          digests.map(([svc, d]) => (
            <div key={svc}>
              {svc}: <code>{d}</code>
            </div>
          ))
        )}
      </dd>
    </dl>
  );
}

// The controller has no dedicated "compare versions" endpoint and no notion of a fixed
// code version. A comparison is expressed with the existing primitives: re-run the same
// scenario version (same seed, same scenario) with a different active_intervention_ids
// set -- the reduced set, or an empty one to model "the fault is gone" -- repeat it, and
// show the outcomes next to the original run.
export default function ComparePanel({ scenario, versionId, baselineRun, reducedIds, onBusyChange }: Props) {
  const [mode, setMode] = useState<"reduced" | "none" | "all">(reducedIds ? "reduced" : "none");
  const [repeats, setRepeats] = useState(MIN_REPEATS_FOR_CONCLUSION);
  const [runs, setRuns] = useState<Run[]>([]);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState<ErrorView | null>(null);
  const currentRunId = useRef<string | null>(null);
  const cancelRequested = useRef(false);
  const alive = useRef(true);
  const abort = useRef(new AbortController());

  useEffect(() => {
    alive.current = true;
    abort.current = new AbortController(); // StrictMode re-runs effects after a cleanup
    return () => {
      alive.current = false;
      abort.current.abort(); // stop polling a run nobody is looking at any more
    };
  }, []);

  useEffect(() => {
    onBusyChange?.(running);
    return () => onBusyChange?.(false);
  }, [running, onBusyChange]);

  // Offer a reduction result when it appears; fall back when it goes away (a replayed
  // run clears it) so the select never shows one option while another would be sent.
  useEffect(() => {
    if (reducedIds) setMode("reduced");
    else setMode((m) => (m === "reduced" ? "none" : m));
  }, [reducedIds]);

  function idsForMode(): string[] {
    if (mode === "none") return [];
    if (mode === "reduced") return reducedIds ?? [];
    return scenario.interventions.map((iv) => iv.id);
  }

  async function handleRun() {
    setError(null);
    setRuns([]);
    cancelRequested.current = false;
    setRunning(true);
    const ids = idsForMode();
    const batch = Date.now();
    const ac = abort.current;
    try {
      for (let i = 0; i < repeats && !cancelRequested.current; i++) {
        const res = await startRunWhenFree({
          version_id: versionId,
          idempotency_key: `compare-${mode}-${batch}-${i}`,
          active_intervention_ids: ids,
        });
        currentRunId.current = res.run_id;
        // Cancel was clicked while this run was being started: it has no id to cancel yet.
        if (cancelRequested.current) await cancelRun(res.run_id).catch(() => undefined);
        const done = await waitForRun(res.run_id, { signal: ac.signal });
        currentRunId.current = null;
        if (!alive.current) return;
        setRuns((prev) => [...prev, done]);
        if (done.state !== "COMPLETED") break; // cancelled or infra error: do not keep repeating
      }
    } catch (e) {
      if (alive.current) setError(describeError(e));
    } finally {
      currentRunId.current = null;
      if (alive.current) setRunning(false);
    }
  }

  async function handleCancel() {
    cancelRequested.current = true; // also stops the loop between two repeats
    const id = currentRunId.current;
    if (!id) return;
    try {
      await cancelRun(id);
    } catch (e) {
      setError(describeError(e));
    }
  }

  const summary = summarizeRepeats(runs, baselineRun);
  const versionMismatch = runs.some((r) => r.version_id !== baselineRun.version_id);

  return (
    <div className="card">
      <h2>5. Compare versions</h2>
      <p className="muted small">
        Re-runs the same scenario version (same seed and scenario) with a different active intervention set, repeats
        it, and shows each result next to the original run. This models a fix by removing the fault; it does not
        test a different build of the service.
      </p>
      <label htmlFor="compare-mode">Candidate intervention set</label>
      <select
        id="compare-mode"
        value={mode}
        onChange={(e) => setMode(e.target.value as typeof mode)}
        disabled={running}
      >
        <option value="none">None active (&quot;the fault is gone&quot;)</option>
        {reducedIds && <option value="reduced">Reduced set ({reducedIds.length})</option>}
        <option value="all">All interventions (same as original)</option>
      </select>
      <label htmlFor="compare-repeats">Repetitions</label>
      <select
        id="compare-repeats"
        value={repeats}
        onChange={(e) => setRepeats(Number(e.target.value))}
        disabled={running}
      >
        {REPEAT_OPTIONS.map((n) => (
          <option key={n} value={n}>
            {n}
            {n < MIN_REPEATS_FOR_CONCLUSION ? " (no conclusion will be drawn)" : ""}
          </option>
        ))}
      </select>
      <div className="row">
        <button onClick={handleRun} disabled={running}>
          {running ? `Running ${runs.length + 1} of ${repeats}...` : "Run comparison"}
        </button>
        <button className="secondary" onClick={handleCancel} disabled={!running}>
          Cancel comparison
        </button>
      </div>

      {error && (
        <div className={`banner ${error.code === "STAND_BUSY" ? "banner-warning" : "banner-error"}`} role="alert">
          <strong>{error.code}</strong>
          <p>{error.message}</p>
          {error.correlation_id && <p className="small muted">correlation_id: {error.correlation_id}</p>}
        </div>
      )}

      <div className="compare-grid">
        <div>
          <h3>Before (original run)</h3>
          <StatusBadge state={baselineRun.state} outcome={baselineRun.outcome} />
          <p className="small muted">{baselineRun.active_intervention_ids.length} intervention(s) active, 1 run</p>
          <Environment scenario={scenario} versionId={baselineRun.version_id} />
        </div>
        <div>
          <h3>After (candidate set)</h3>
          {runs.length === 0 ? (
            <p className="muted small">{running ? "Waiting for the first result..." : "Not run yet."}</p>
          ) : (
            <>
              <ul className="plain-list">
                {runs.map((r, i) => (
                  <li key={r.run_id}>
                    #{i + 1} <StatusBadge state={r.state} outcome={r.outcome} />
                  </li>
                ))}
              </ul>
              <p className="small muted">{runs[0].active_intervention_ids.length} intervention(s) active</p>
              <Environment scenario={scenario} versionId={runs[0].version_id} />
              {runs.length === 1 && <p className="explain">{explainRun(runs[0].state, runs[0].outcome)}</p>}
            </>
          )}
        </div>
      </div>

      {versionMismatch && (
        <div className="banner banner-error" role="alert">
          <strong>Different scenario versions</strong>
          <p>The compared runs do not share a version_id, so this is not a like-for-like comparison.</p>
        </div>
      )}
      {!running && summary.text && (
        <div className={`banner ${summary.verdict === "no_failure_observed" ? "banner-success" : "banner-neutral"}`}>
          <strong>Result of the candidate set</strong>
          <p>{summary.text}</p>
        </div>
      )}
    </div>
  );
}
