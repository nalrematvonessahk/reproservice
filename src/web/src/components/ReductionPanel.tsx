import { useState } from "react";
import type { ReduceAlgorithm, ReduceResult, Run } from "../types";
import { explainStopReason } from "../explain";

interface Props {
  run: Run | null;
  onReduce: (algorithm: ReduceAlgorithm) => void;
  // FR09: a reduction runs inside one long HTTP request; cancelling it goes through
  // POST /api/runs/{parent run id}/cancel, so the parent run's Cancel button (disabled
  // once the run is COMPLETED) cannot do it.
  onCancel: () => void;
  busy: boolean;
  result: ReduceResult | null;
  error: { code: string; message: string; correlation_id: string } | null;
}

const BUDGET_EXHAUSTED = new Set(["BUDGET_EXHAUSTED_RUNS", "BUDGET_EXHAUSTED_SECONDS"]);

export default function ReductionPanel({ run, onReduce, onCancel, busy, result, error }: Props) {
  const [algorithm, setAlgorithm] = useState<ReduceAlgorithm>("ddmin");
  const canReduce = run !== null && run.state === "COMPLETED";
  // result.confirmed === false: even the full set was not confirmed, so the "accepted"
  // list is only the input, not a reduced result.
  const confirmed = result?.confirmed ?? true;
  const oneMinimal = result?.one_minimal ?? result?.stop_reason === "minimal";

  return (
    <div className="card">
      <h2>4. Reduce</h2>
      <p className="muted small">
        Finds a 1-minimal subset of the active interventions that still reproduces ORIGINAL_FAILURE under the same
        conditions. Not available until the run has reached a verdict.
      </p>
      <label htmlFor="algo-select">Algorithm</label>
      <select
        id="algo-select"
        value={algorithm}
        onChange={(e) => setAlgorithm(e.target.value as ReduceAlgorithm)}
        disabled={!canReduce || busy}
      >
        <option value="ddmin">ddmin</option>
        <option value="sequential">sequential</option>
        <option value="dependency_aware">dependency_aware</option>
      </select>
      <div className="row">
        <button onClick={() => onReduce(algorithm)} disabled={!canReduce || busy}>
          {busy ? "Reducing..." : "Reduce"}
        </button>
        <button className="secondary" onClick={onCancel} disabled={!busy}>
          Cancel reduction
        </button>
      </div>
      {busy && (
        <p className="muted small" role="status">
          Every candidate is a full reset + run, so this can take minutes. Cancel stops new candidates; the best
          candidate confirmed so far is still reported.
        </p>
      )}
      {!canReduce && !result && <p className="muted small">Run must reach COMPLETED before it can be reduced.</p>}

      {error && (
        <div className="banner banner-error" role="alert">
          <strong>{error.code}</strong>
          <p>{error.message}</p>
        </div>
      )}

      {result && (
        <div
          className={`banner ${
            oneMinimal && !result.unstable
              ? "banner-success"
              : BUDGET_EXHAUSTED.has(result.stop_reason)
              ? "banner-warning"
              : "banner-neutral"
          }`}
        >
          <strong>stop_reason: {result.stop_reason}</strong>
          <p>{explainStopReason(result.stop_reason)}</p>
          <dl className="kv">
            <dt>algorithm</dt>
            <dd>{result.algorithm}</dd>
            <dt>runs_used</dt>
            <dd>{result.runs_used}</dd>
            {result.max_runs !== undefined && (
              <>
                <dt>budget</dt>
                <dd>
                  {result.runs_used} of {result.max_runs} runs
                  {result.confirmation_runs !== undefined && ` (${result.confirmation_runs} confirmation)`}
                  {result.cache_hits ? `, ${result.cache_hits} cache hit(s)` : ""}
                  {result.skipped_invalid ? `, ${result.skipped_invalid} invalid candidate(s) skipped` : ""}
                </dd>
              </>
            )}
            <dt>{confirmed ? "accepted_intervention_ids" : "accepted_intervention_ids (input set, NOT confirmed)"}</dt>
            <dd>
              {result.accepted_intervention_ids.length > 0 ? (
                <ul className="plain-list">
                  {result.accepted_intervention_ids.map((id) => (
                    <li key={id}>
                      <code>{id}</code>
                    </li>
                  ))}
                </ul>
              ) : (
                <span className="muted">(none)</span>
              )}
            </dd>
            <dt>1-minimal</dt>
            <dd>{oneMinimal ? "yes (not a proof of global minimum)" : "no -- the search did not finish"}</dd>
            <dt>unstable</dt>
            <dd>{result.unstable ? "yes -- fluctuated under repeat confirmation" : "no"}</dd>
          </dl>
          {result.error && <p className="small">{result.error}</p>}
        </div>
      )}
    </div>
  );
}
