import type { Intervention, Scenario } from "./types";

// The controller accepts scenarios in which optional fields are omitted (depends_on,
// image_digests, seed_hash ...) and the UI keeps the JSON the user pasted, not the
// controller's normalised copy. Fill the optional fields so components can rely on
// the Scenario type instead of crashing on `undefined.length`.
export function normalizeScenario(raw: unknown): Scenario {
  const sc = raw as Scenario;
  const interventions: Intervention[] = (sc.interventions ?? []).map((iv) => ({
    ...iv,
    depends_on: iv.depends_on ?? [],
  }));
  return {
    ...sc,
    environment: { ...sc.environment, image_digests: sc.environment?.image_digests ?? {} },
    interventions,
  };
}
