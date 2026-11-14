import { useState } from "react";
import type { ReduceAlgorithm, ReduceResult, Run } from "../types";
import { explainStopReason } from "../explain";

interface Props {
  run: Run | null;
  onReduce: (algorithm: ReduceAlgorithm) => void;
  busy: boolean;
  result: ReduceResult | null;
  error: { code: string; message: string; correlation_id: string } | null;
}

const BUDGET_EXHAUSTED = new Set(["BUDGET_EXHAUSTED_RUNS", "BUDGET_EXHAUSTED_SECONDS"]);

export default function ReductionPanel({ run, onReduce, busy, result, error }: Props) {
  const [algorithm, setAlgorithm] = useState<ReduceAlgorithm>("ddmin");
  const canReduce = run !== null && run.state === "COMPLETED";

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
      </div>
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
            result.stop_reason === "minimal" && !result.unstable
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
            <dt>accepted_intervention_ids</dt>
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
            <dt>unstable</dt>
            <dd>{result.unstable ? "yes -- fluctuated under repeat confirmation" : "no"}</dd>
          </dl>
        </div>
      )}
    </div>
  );
}
