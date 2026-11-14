import { useState } from "react";
import { ApiError, createRun } from "../api";
import type { Run, Scenario } from "../types";
import { useRunPolling } from "../hooks";
import StatusBadge from "./StatusBadge";
import { explainRun } from "../explain";

interface Props {
  scenario: Scenario;
  versionId: string;
  baselineRun: Run;
  reducedIds: string[] | null;
}

// The controller has no dedicated "compare versions" endpoint. A version comparison
// is expressed with the existing primitives: re-run the same scenario version with a
// different active_intervention_ids set (e.g. the reduced set, or none at all to
// simulate "if this were fixed") and compare the two Run outcomes side by side.
export default function ComparePanel({ scenario, versionId, baselineRun, reducedIds }: Props) {
  const [mode, setMode] = useState<"reduced" | "none" | "all">(reducedIds ? "reduced" : "none");
  const [compareRunId, setCompareRunId] = useState<string | null>(null);
  const [requestedEmpty, setRequestedEmpty] = useState(false);
  const [starting, setStarting] = useState(false);
  const [startError, setStartError] = useState<{ code: string; message: string; correlation_id: string } | null>(
    null
  );
  const { run: compareRun, error: pollError } = useRunPolling(compareRunId);

  function idsForMode(): string[] {
    if (mode === "none") return [];
    if (mode === "reduced" && reducedIds) return reducedIds;
    return scenario.interventions.map((iv) => iv.id);
  }

  async function handleRun() {
    setStartError(null);
    setStarting(true);
    try {
      const ids = idsForMode();
      setRequestedEmpty(ids.length === 0);
      const res = await createRun({
        version_id: versionId,
        idempotency_key: `compare-${mode}-${Date.now()}`,
        active_intervention_ids: ids,
      });
      setCompareRunId(res.run_id);
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

  const activeCompare = compareRun !== null && !["COMPLETED", "CANCELLED", "INFRA_ERROR"].includes(compareRun.state);

  return (
    <div className="card">
      <h2>5. Compare versions</h2>
      <p className="muted small">
        Re-runs the same scenario version with a different active intervention set and compares the outcome against
        the original run above.
      </p>
      <label htmlFor="compare-mode">Candidate intervention set</label>
      <select id="compare-mode" value={mode} onChange={(e) => setMode(e.target.value as typeof mode)} disabled={activeCompare}>
        <option value="none">None requested ("if this were fixed")</option>
        {reducedIds && <option value="reduced">Reduced set ({reducedIds.length})</option>}
        <option value="all">All interventions (same as original)</option>
      </select>
      {mode === "none" && (
        <p className="muted small">
          Note: POST /api/runs treats an empty active_intervention_ids list the same as omitting it, so the
          controller will actually activate every intervention in the scenario -- there is currently no way to
          request zero active interventions via this endpoint. The result below will call this out if it happens.
        </p>
      )}
      <div className="row">
        <button onClick={handleRun} disabled={starting || activeCompare}>
          {starting ? "Starting..." : "Run comparison"}
        </button>
      </div>

      {startError && (
        <div className={`banner ${startError.code === "STAND_BUSY" ? "banner-warning" : "banner-error"}`}>
          <strong>{startError.code}</strong>
          <p>{startError.message}</p>
        </div>
      )}
      {pollError && (
        <div className="banner banner-error">
          <strong>{pollError.code}</strong>
          <p>{pollError.message}</p>
        </div>
      )}

      {compareRun && requestedEmpty && compareRun.active_intervention_ids.length > 0 && (
        <div className="banner banner-warning">
          <strong>Request did not take effect as intended</strong>
          <p>
            Requested zero active interventions, but the controller defaulted to all{" "}
            {compareRun.active_intervention_ids.length} because the API has no representation for "empty" distinct
            from "omitted". The outcome below reflects the full fault set, not a fixed/no-fault run.
          </p>
        </div>
      )}

      {compareRun && (
        <div className="compare-grid">
          <div>
            <h3>Before</h3>
            <StatusBadge state={baselineRun.state} outcome={baselineRun.outcome} />
            <p className="small muted">{baselineRun.active_intervention_ids.length} intervention(s) active</p>
          </div>
          <div>
            <h3>After</h3>
            <StatusBadge state={compareRun.state} outcome={compareRun.outcome} />
            <p className="small muted">{compareRun.active_intervention_ids.length} intervention(s) active</p>
            <p className="explain">{explainRun(compareRun.state, compareRun.outcome)}</p>
          </div>
        </div>
      )}
    </div>
  );
}
