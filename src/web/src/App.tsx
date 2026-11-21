import { useRef, useState } from "react";
import ScenarioPanel from "./components/ScenarioPanel";
import RunPanel from "./components/RunPanel";
import EventTimeline from "./components/EventTimeline";
import ReductionPanel from "./components/ReductionPanel";
import ComparePanel from "./components/ComparePanel";
import ExportPanel from "./components/ExportPanel";
import { API_BASE, cancelRun, createRun, describeError, isExistingRun, reduceRun } from "./api";
import type { ErrorView } from "./api";
import { useEventPolling, useRunPolling } from "./hooks";
import { isTerminalState } from "./types";
import type { ReduceAlgorithm, ReduceResult, Scenario } from "./types";

function newIdempotencyKey(): string {
  return `ui-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

type ErrLike = ErrorView;

export default function App() {
  const [scenario, setScenario] = useState<Scenario | null>(null);
  const [versionId, setVersionId] = useState<string | null>(null);
  const [activeIds, setActiveIds] = useState<Set<string>>(new Set());
  const [idempotencyKey, setIdempotencyKey] = useState(newIdempotencyKey());

  const [runId, setRunId] = useState<string | null>(null);
  const [starting, setStarting] = useState(false);
  const [startError, setStartError] = useState<ErrLike | null>(null);
  const [startNotice, setStartNotice] = useState<string | null>(null);
  const { run, error: pollError } = useRunPolling(runId);
  const events = useEventPolling(runId, run?.state ?? null);

  const [reducing, setReducing] = useState(false);
  const [reduceResult, setReduceResult] = useState<ReduceResult | null>(null);
  const [reduceError, setReduceError] = useState<ErrLike | null>(null);
  const [comparing, setComparing] = useState(false);

  const runError = startError ?? pollError;
  // The run the page currently shows; lets a slow reduce response detect that the user
  // has moved on to another run or scenario and drop its result instead of showing it there.
  const currentRunId = useRef<string | null>(null);
  currentRunId.current = runId;
  const runActive = run !== null && !isTerminalState(run.state);
  // Anything that holds the stand and is controlled from a panel that Reset or loading
  // another scenario would unmount: keep those controls locked until it ends.
  const standHeld = runActive || reducing || comparing;

  function handleScenarioLoaded(sc: Scenario, vid: string) {
    setScenario(sc);
    setVersionId(vid);
    setActiveIds(new Set(sc.interventions.map((iv) => iv.id)));
    setRunId(null);
    setStartError(null);
    setStartNotice(null);
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
    setStartNotice(null);
    setReduceResult(null);
    setReduceError(null);
    setStarting(true);
    try {
      // Send ids in scenario order so the same selection always produces the same request.
      const requested = scenario!.interventions.map((iv) => iv.id).filter((id) => activeIds.has(id));
      const res = await createRun({
        version_id: versionId,
        idempotency_key: idempotencyKey,
        active_intervention_ids: requested,
      });
      if (isExistingRun(res)) {
        // 200 instead of 202: the key was already used, nothing new was started.
        const same = res.active_intervention_ids.join(",") === requested.join(",");
        const sameVersion = res.version_id === versionId;
        setStartNotice(
          `This idempotency key was already used, so run ${res.run_id} is shown instead of a new run.` +
            (sameVersion ? "" : ` It belongs to a different scenario version (${res.version_id}), not the one loaded now (${versionId}).`) +
            (same
              ? ""
              : ` It was started with ${res.active_intervention_ids.length} intervention(s), not the ${requested.length} selected now.`) +
            (sameVersion && same ? "" : ` Use "Start a different run" for a new key.`)
        );
      }
      setRunId(res.run_id);
    } catch (e) {
      setStartError(describeError(e));
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

  // Reductions have no polling that would surface a failed cancel, so report it here.
  async function handleCancelReduction() {
    if (!runId) return;
    try {
      const res = await cancelRun(runId);
      if (res.status === "not_running") {
        setReduceError({
          code: "CANCEL_NOT_APPLIED",
          message: "The controller reports nothing to cancel yet. If the reduction is still running, try again.",
          correlation_id: "",
        });
      }
    } catch (e) {
      setReduceError(describeError(e));
    }
  }

  async function handleReduce(algorithm: ReduceAlgorithm) {
    if (!runId) return;
    setReducing(true);
    setReduceError(null);
    const forRun = runId;
    try {
      const res = await reduceRun(forRun, algorithm);
      if (currentRunId.current === forRun) {
        setReduceResult(res);
        setReduceError(null);
      }
    } catch (e) {
      if (currentRunId.current === forRun) setReduceError(describeError(e));
    } finally {
      setReducing(false);
    }
  }

  function handleNewRun() {
    setRunId(null);
    setStartError(null);
    setStartNotice(null);
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
    setStartNotice(null);
    setReduceResult(null);
    setReduceError(null);
    setIdempotencyKey(newIdempotencyKey());
  }

  return (
    <div className="app">
      <header>
        <div>
          <h1 className="brand">ReproService</h1>
          <span className="tag">REPRODUCE &middot; REDUCE &middot; VERIFY</span>
        </div>
        <button
          className="secondary"
          onClick={handleFullReset}
          disabled={reducing || comparing}
          title={reducing || comparing ? "Cancel the reduction/comparison in progress first" : undefined}
        >
          Reset
        </button>
      </header>
      <aside className="notice" aria-label="Controller address">
        Live controller at <code>{API_BASE}</code>. All data below comes from real API calls against the
        running Docker Compose stand -- nothing on this page is synthetic.
      </aside>

      <main>
        {!scenario && (
          <div className="card empty-state">
            <h2>No scenario loaded</h2>
            <p className="muted">
              Pick a built-in example or paste your own scenario JSON below, then validate it to get a version_id.
            </p>
          </div>
        )}

        <ScenarioPanel onLoaded={handleScenarioLoaded} disabled={standHeld} />

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
              notice={startNotice}
            />

            {run && (
              <div className="row" style={{ margin: "8px 0 24px" }}>
                <button className="secondary" onClick={handleNewRun} disabled={standHeld}>
                  Start a different run
                </button>
              </div>
            )}

            <EventTimeline events={events} scenario={scenario} />

            {run && (
              <ReductionPanel run={run} onReduce={handleReduce} onCancel={handleCancelReduction} busy={reducing} result={reduceResult} error={reduceError} />
            )}

            {run && run.state === "COMPLETED" && (
              <ComparePanel
                scenario={scenario}
                versionId={versionId}
                baselineRun={run}
                reducedIds={reduceResult ? reduceResult.accepted_intervention_ids : null}
                onBusyChange={setComparing}
              />
            )}

            {run && isTerminalState(run.state) && (
              <ExportPanel key={run.run_id} run={run} scenario={scenario} reduction={reduceResult} />
            )}
          </>
        )}
      </main>
      <footer>SE-2416 &middot; ReproService web UI &middot; talks to a live controller, no mocks</footer>
    </div>
  );
}
