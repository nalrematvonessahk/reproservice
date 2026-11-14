import { useState } from "react";
import { ApiError, exportRun } from "../api";
import type { ExportPayload, Run } from "../types";

interface Props {
  run: Run;
}

export default function ExportPanel({ run }: Props) {
  const [data, setData] = useState<ExportPayload | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<{ code: string; message: string } | null>(null);

  async function handleExport() {
    setBusy(true);
    setError(null);
    try {
      const res = await exportRun(run.run_id);
      setData(res);
    } catch (e) {
      if (e instanceof ApiError) {
        setError({ code: e.body.code, message: e.body.message });
      } else {
        setError({ code: "UNKNOWN_ERROR", message: String(e) });
      }
    } finally {
      setBusy(false);
    }
  }

  function handleDownload() {
    if (!data) return;
    const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = `reproservice-${run.run_id}.json`;
    a.click();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
  }

  return (
    <div className="card">
      <h2>6. Export evidence</h2>
      <p className="muted small">Fetches the run plus its full (already redacted) event log as JSON.</p>
      <div className="row">
        <button onClick={handleExport} disabled={busy}>
          {busy ? "Exporting..." : "Export JSON"}
        </button>
        <button className="secondary" onClick={handleDownload} disabled={!data}>
          Download file
        </button>
      </div>
      {error && (
        <div className="banner banner-error">
          <strong>{error.code}</strong>
          <p>{error.message}</p>
        </div>
      )}
      {data && <pre className="export-pre">{JSON.stringify(data, null, 2)}</pre>}
    </div>
  );
}
