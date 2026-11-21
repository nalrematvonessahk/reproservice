import { useState } from "react";
import { createScenario, describeError } from "../api";
import type { ErrorView } from "../api";
import { normalizeScenario } from "../scenario";
import { BUILTIN_EXAMPLES } from "../examples";
import type { Scenario } from "../types";

interface Props {
  onLoaded: (scenario: Scenario, versionId: string) => void;
  disabled: boolean;
}

export default function ScenarioPanel({ onLoaded, disabled }: Props) {
  const [text, setText] = useState(() => JSON.stringify(BUILTIN_EXAMPLES[0].scenario, null, 2));
  const [exampleKey, setExampleKey] = useState(BUILTIN_EXAMPLES[0].key);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ErrorView | null>(null);

  function pickExample(key: string) {
    setExampleKey(key);
    const found = BUILTIN_EXAMPLES.find((e) => e.key === key);
    if (found) {
      setText(JSON.stringify(found.scenario, null, 2));
      setError(null);
    }
  }

  async function handleLoad() {
    setError(null);
    let parsed: unknown;
    try {
      parsed = JSON.parse(text);
    } catch (e) {
      setError({
        code: "INVALID_JSON",
        message: e instanceof Error ? e.message : "could not parse JSON",
        correlation_id: "",
      });
      return;
    }
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
      setError({ code: "INVALID_JSON", message: "A scenario must be a JSON object.", correlation_id: "" });
      return;
    }
    setBusy(true);
    try {
      const res = await createScenario(parsed);
      onLoaded(normalizeScenario(parsed), res.version_id);
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(false);
    }
  }

  function handleUpload(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    if (!file) return;
    const reader = new FileReader();
    reader.onerror = () =>
      setError({ code: "FILE_READ_ERROR", message: `Could not read ${file.name}.`, correlation_id: "" });
    reader.onload = () => {
      setText(String(reader.result ?? ""));
      setError(null);
    };
    reader.readAsText(file);
    e.target.value = "";
  }

  return (
    <div className="card">
      <h2>1. Scenario</h2>
      <label htmlFor="example-select">Built-in example</label>
      <select id="example-select" value={exampleKey} onChange={(e) => pickExample(e.target.value)} disabled={disabled}>
        {BUILTIN_EXAMPLES.map((ex) => (
          <option key={ex.key} value={ex.key}>
            {ex.label}
          </option>
        ))}
      </select>
      <p className="muted small">{BUILTIN_EXAMPLES.find((e) => e.key === exampleKey)?.description}</p>

      <label htmlFor="scenario-text">Scenario JSON (paste or edit)</label>
      <textarea
        id="scenario-text"
        value={text}
        onChange={(e) => setText(e.target.value)}
        rows={14}
        spellCheck={false}
        disabled={disabled}
      />

      <div className="row">
        <label htmlFor="scenario-upload" className="file-label">
          Upload file
          <input id="scenario-upload" type="file" accept=".json,application/json" onChange={handleUpload} disabled={disabled} />
        </label>
        <button onClick={handleLoad} disabled={busy || disabled}>
          {busy ? "Validating..." : "Validate & load scenario"}
        </button>
      </div>

      {error && (
        <div className="banner banner-error" role="alert">
          <strong>{error.code}</strong>
          <p>{error.message}</p>
          {error.field && <p className="small">field: {error.field}</p>}
          {error.correlation_id && <p className="small muted">correlation_id: {error.correlation_id}</p>}
        </div>
      )}
    </div>
  );
}
