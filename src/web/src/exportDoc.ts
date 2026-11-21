import type { ExportDocument, ExportPayload, ReduceResult, Run, Scenario } from "./types";

// The controller caps GET /api/runs/{id}/export at this many events and does not say
// when it truncated, so a result of exactly this size must be flagged to the user.
export const EXPORT_EVENT_LIMIT = 10000;

export function buildExportDocument(
  payload: ExportPayload,
  scenario: Scenario,
  reduction: ReduceResult | null
): ExportDocument {
  const doc: ExportDocument = { ...payload, scenario };
  if (reduction) doc.reduction = reduction;
  return doc;
}

export function exportFilename(run: Run): string {
  const safe = run.run_id.replace(/[^A-Za-z0-9._-]/g, "_");
  return `reproservice-${safe}.json`;
}

export function mayBeTruncated(payload: ExportPayload): boolean {
  return payload.events.length >= EXPORT_EVENT_LIMIT;
}

export function serializeExport(doc: ExportDocument): string {
  return JSON.stringify(doc, null, 2) + "\n";
}
