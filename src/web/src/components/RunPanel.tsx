import { useState } from "react";
import { isTerminalState } from "../types";
import type { Run, Scenario } from "../types";
import StatusBadge from "./StatusBadge";
import { explainRun } from "../explain";

interface Props {
  scenario: Scenario;
  versionId: string;
  activeIds: Set<string>;
  onToggleIntervention: (id: string) => void;
  idempotencyKey: string;
  onIdempotencyKeyChange: (v: string) => void;
  onStart: () => void;
  onCancel: () => void;
  starting: boolean;
  run: Run | null;
  runError: { code: string; message: string; correlation_id: string } | null;
  // Informational message about the last Start click (e.g. an idempotent replay).
  notice: string | null;
}

export default function RunPanel({
  scenario,
  versionId,
  activeIds,
  onToggleIntervention,
  idempotencyKey,
  onIdempotencyKeyChange,
  onStart,
  onCancel,
  starting,
  run,
  runError,
  notice,
}: Props) {
  const [showJson, setShowJson] = useState(false);
  const isActive = run !== null && !isTerminalState(run.state);

  return (
    <div className="card">
      <h2>2. Run</h2>
      <dl className="kv">
        <dt>scenario_id</dt>
        <dd>
          <code>{scenario.id}</code>
        </dd>
        <dt>version_id</dt>
        <dd>
          <code>{versionId}</code>
        </dd>
        <dt>invariant_id</dt>
        <dd>
          <code>{scenario.invariant_id}</code>
        </dd>
        <dt>budget</dt>
        <dd>
          {scenario.budget.runs} runs / {scenario.budget.seconds}s
        </dd>
      </dl>

      <label>Active interventions</label>
      <ul className="intervention-list">
        {scenario.interventions.map((iv) => (
          <li key={iv.id}>
            <label className="checkbox-row">
              <input
                type="checkbox"
                checked={activeIds.has(iv.id)}
                onChange={() => onToggleIntervention(iv.id)}
                disabled={isActive}
              />
              <span>
                <code>{iv.id}</code> &mdash; {iv.action.type} on {iv.selector.source}&rarr;{iv.selector.target}{" "}
                {iv.selector.method} {iv.selector.normalized_path} (attempt {iv.selector.attempt})
                {iv.depends_on.length > 0 && <span className="muted small"> depends_on: {iv.depends_on.join(", ")}</span>}
              </span>
            </label>
          </li>
        ))}
      </ul>

      <label htmlFor="idem-key">Idempotency key</label>
      <input
        id="idem-key"
        value={idempotencyKey}
        onChange={(e) => onIdempotencyKeyChange(e.target.value)}
        disabled={isActive}
      />
      <p className="muted small">
        Re-submitting the same idempotency key for this scenario replays the existing run instead of starting a new
        one.
      </p>

      <div className="row">
        <button onClick={onStart} disabled={starting || isActive || activeIds.size === 0}>
          {starting ? "Starting..." : "Start run"}
        </button>
        <button className="secondary" onClick={onCancel} disabled={!isActive}>
          Cancel run
        </button>
      </div>

      {notice && (
        <div className="banner banner-warning" role="status">
          <p>{notice}</p>
        </div>
      )}

      {runError && (
        <div className={`banner ${runError.code === "STAND_BUSY" ? "banner-warning" : "banner-error"}`} role="alert">
          <strong>{runError.code}</strong>
          <p>{runError.message}</p>
          {runError.correlation_id && <p className="small muted">correlation_id: {runError.correlation_id}</p>}
        </div>
      )}

      <div className="status-block">
        <StatusBadge state={run?.state ?? null} outcome={run?.outcome} />
        <p className="explain">{explainRun(run?.state ?? null, run?.outcome)}</p>
        {run?.stop_reason && (
          <p className="small muted">
            stop_reason: <code>{run.stop_reason}</code>
          </p>
        )}
      </div>

      {run && (
        <>
          <button className="link-button" onClick={() => setShowJson((v) => !v)}>
            {showJson ? "Hide raw run JSON" : "Show raw run JSON"}
          </button>
          {showJson && <pre tabIndex={0} role="region" aria-label="Raw run JSON">{JSON.stringify(run, null, 2)}</pre>}
        </>
      )}
    </div>
  );
}
