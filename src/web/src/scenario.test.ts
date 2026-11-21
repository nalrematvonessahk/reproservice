import { describe, expect, it } from "vitest";
import { normalizeScenario } from "./scenario";
import { BUILTIN_EXAMPLES } from "./examples";

describe("normalizeScenario", () => {
  it("fills depends_on and image_digests that the controller accepts as omitted", () => {
    const sc = normalizeScenario({
      schema_version: 1,
      id: "s",
      environment: { compose_profile: "p", seed_id: "x" },
      invariant_id: "i",
      budget: { runs: 1, seconds: 1 },
      interventions: [{ id: "a", selector: {}, action: { type: "drop_response_after_success" } }],
    });
    expect(sc.interventions[0].depends_on).toEqual([]);
    expect(sc.environment.image_digests).toEqual({});
  });

  it("leaves the built-in examples semantically unchanged", () => {
    for (const ex of BUILTIN_EXAMPLES) {
      const sc = normalizeScenario(ex.scenario);
      expect(sc.interventions.length).toBeGreaterThan(0);
      for (const iv of sc.interventions) expect(Array.isArray(iv.depends_on)).toBe(true);
    }
  });
});
