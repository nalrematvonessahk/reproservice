import { useState } from "react";
import ScenarioPanel from "./components/ScenarioPanel";
import RunPanel from "./components/RunPanel";
import EventTimeline from "./components/EventTimeline";
import ReductionPanel from "./components/ReductionPanel";
import ComparePanel from "./components/ComparePanel";
import ExportPanel from "./components/ExportPanel";
import { ApiError, cancelRun, createRun, reduceRun } from "./api";
import { useEventPolling, useRunPolling } from "./hooks";
import type { ReduceAlgorithm, ReduceResult, Scenario } from "./types";

function newIdempotencyKey(): string {
  return `ui-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

interface ErrLike {
  code: string;
  message: string;
  correlation_id: string;
}

export default function App() {
  const [scenario, setScenario] = useState<Scenario | null>(null);
  const [versionId, setVersionId] = useState<string | null>(null);
  const [activeIds, setActiveIds] = useState<Set<string>>(new Set());
  const [idempotencyKey, setIdempotencyKey] = useState(newIdempotencyKey());

  const [runId, setRunId] = useState<string | null>(null);
  const [starting, setStarting] = useState(false);
  const [startError, setStartError] = useState<ErrLike | null>(null);
  const { run, error: pollError } = useRunPolling(runId);
  const events = useEventPolling(runId, run?.state ?? null);

  const [reducing, setReducing] = useState(false);
  const [reduceResult, setReduceResult] = useState<ReduceResult | null>(null);
  const [reduceError, setReduceError] = useState<ErrLike | null>(null);

  const runError = startError ?? pollError;

  function handleScenarioLoaded(sc: Scenario, vid: string) {
    setScenario(sc);
    setVersionId(vid);
    setActiveIds(new Set(sc.interventions.map((iv) => iv.id)));
    setRunId(null);
    setStartError(null);
    setReduceResult(null);
    setReduceError(null);
  }

  function toggleIntervention(id: string) {
    setActiveIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  async function handleStart() {
    if (!versionId) return;
    setStartError(null);
    setReduceResult(null);
    setReduceError(null);
    setStarting(true);
    try {
      const res = await createRun({
        version_id: versionId,
        idempotency_key: idempotencyKey,
        active_intervention_ids: Array.from(activeIds),
      });
      setRunId(res.run_id);
    } catch (e) {
      if (e instanceof ApiError) {
        setStartError({ code: e.body.code, message: e.body.message, correlation_id: e.body.correlation_id });
      } else {
        setStartError({ code: "UNKNOWN_ERROR", message: String(e), correlation_id: "" });
      }
    } finally {
      setStarting(false);
    }
  }

  async function handleCancel() {
    if (!runId) return;
    try {
      await cancelRun(runId);
    } catch {
      // the next poll tick will surface any real problem via pollError
    }
  }

  async function handleReduce(algorithm: ReduceAlgorithm) {
    if (!runId) return;
    setReducing(true);
    setReduceError(null);
    try {
      const res = await reduceRun(runId, algorithm);
      setReduceResult(res);
    } catch (e) {
      if (e instanceof ApiError) {
        setReduceError({ code: e.body.code, message: e.body.message, correlation_id: e.body.correlation_id });
      } else {
        setReduceError({ code: "UNKNOWN_ERROR", message: String(e), correlation_id: "" });
      }
    } finally {
      setReducing(false);
    }
  }

  function handleNewRun() {
    setRunId(null);
    setStartError(null);
    setReduceResult(null);
    setReduceError(null);
    setIdempotencyKey(newIdempotencyKey());
  }

  function handleFullReset() {
    setScenario(null);
    setVersionId(null);
    setActiveIds(new Set());
    setRunId(null);
    setStartError(null);
    setReduceResult(null);
    setReduceError(null);
    setIdempotencyKey(newIdempotencyKey());
  }

  return (
    <div className="app">
      <header>
        <div>
          <b>ReproService</b>
          <span className="tag">REPRODUCE &middot; REDUCE &middot; VERIFY</span>
        </div>
        <button className="secondary" onClick={handleFullReset}>
          Reset
        </button>
      </header>
      <div className="notice">
        Live controller at <code>http://127.0.0.1:7000</code>. All data below comes from real API calls against the
        running Docker Compose stand -- nothing on this page is synthetic.
      </div>

      <main>
        {!scenario && (
          <div className="card empty-state">
            <h2>No scenario loaded</h2>
            <p className="muted">
              Pick a built-in example or paste your own scenario JSON below, then validate it to get a version_id.
            </p>
          </div>
        )}

        <ScenarioPanel onLoaded={handleScenarioLoaded} disabled={run !== null && !["COMPLETED", "CANCELLED", "INFRA_ERROR"].includes(run.state)} />

        {scenario && versionId && (
          <>
            <RunPanel
              scenario={scenario}
              versionId={versionId}
              activeIds={activeIds}
              onToggleIntervention={toggleIntervention}
              idempotencyKey={idempotencyKey}
              onIdempotencyKeyChange={setIdempotencyKey}
              onStart={handleStart}
              onCancel={handleCancel}
              starting={starting}
              run={run}
              runError={runError}
            />

            {run && (
              <div className="row" style={{ margin: "8px 0 24px" }}>
                <button className="secondary" onClick={handleNewRun}>
                  Start a different run
                </button>
              </div>
            )}

            <EventTimeline events={events} />

            {run && (
              <ReductionPanel run={run} onReduce={handleReduce} busy={reducing} result={reduceResult} error={reduceError} />
            )}

            {run && run.state === "COMPLETED" && (
              <ComparePanel
                scenario={scenario}
                versionId={versionId}
                baselineRun={run}
                reducedIds={reduceResult ? reduceResult.accepted_intervention_ids : null}
              />
            )}

            {run && run.state === "COMPLETED" && <ExportPanel run={run} />}
          </>
        )}
      </main>
      <footer>SE-2416 &middot; ReproService web UI &middot; talks to a live controller, no mocks</footer>
    </div>
  );
}
