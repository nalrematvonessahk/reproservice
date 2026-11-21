import { useState } from "react";
import { describeError, exportRun } from "../api";
import type { ErrorView } from "../api";
import { buildExportDocument, exportFilename, mayBeTruncated, serializeExport } from "../exportDoc";
import type { ExportDocument, ReduceResult, Run, Scenario } from "../types";

interface Props {
  run: Run;
  scenario: Scenario;
  reduction: ReduceResult | null;
}

export default function ExportPanel({ run, scenario, reduction }: Props) {
  const [doc, setDoc] = useState<ExportDocument | null>(null);
  const [truncated, setTruncated] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ErrorView | null>(null);

  async function handleExport() {
    setBusy(true);
    setError(null);
    try {
      const payload = await exportRun(run.run_id);
      setDoc(buildExportDocument(payload, scenario, reduction));
      setTruncated(mayBeTruncated(payload));
    } catch (e) {
      setDoc(null);
      setError(describeError(e));
    } finally {
      setBusy(false);
    }
  }

  function handleDownload() {
    if (!doc) return;
    const blob = new Blob([serializeExport(doc)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = exportFilename(doc.run);
    // Some browsers only honour click() on an anchor that is attached to the page.
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  return (
    <div className="card">
      <h2>6. Export evidence</h2>
      <p className="muted small">
        Fetches the run and its full event log (the proxy redacts sensitive headers before logging; the log holds no
        headers or bodies) and adds the scenario configuration
        {reduction ? " and the reduction result" : ""} shown on this page. Export again after reducing to include
        the reduction.
      </p>
      <div className="row">
        <button onClick={handleExport} disabled={busy}>
          {busy ? "Exporting..." : doc ? "Refresh export" : "Export JSON"}
        </button>
        <button className="secondary" onClick={handleDownload} disabled={!doc}>
          Download file
        </button>
      </div>
      {error && (
        <div className="banner banner-error" role="alert">
          <strong>{error.code}</strong>
          <p>{error.message}</p>
          {error.correlation_id && <p className="small muted">correlation_id: {error.correlation_id}</p>}
        </div>
      )}
      {doc && (
        <>
          <p className="small muted">
            {doc.events.length} event(s) &middot; schema_version {doc.schema_version} &middot; outcome{" "}
            {doc.run.outcome || doc.run.state}
            {doc.reduction ? " · includes reduction" : ""}
            {doc.events.length === 0 ? " · this run produced no events" : ""}
          </p>
          {truncated && (
            <div className="banner banner-warning" role="alert">
              <strong>Event log may be truncated</strong>
              <p>The controller returns at most 10000 events and does not say when it cuts the list.</p>
            </div>
          )}
          <pre className="export-pre" tabIndex={0} role="region" aria-label="Export JSON preview">{serializeExport(doc)}</pre>
        </>
      )}
    </div>
  );
}
