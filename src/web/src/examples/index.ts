// Built-in example scenarios, copied verbatim from the repo's config/ directory
// (config/scenario.example.json and config/scenarios/multi-fault-demo.json) so the
// UI can offer them as one-click starting points. These are plain data copies, not
// a build-time reference outside src/web -- keep them in sync by hand if the
// source files change.
import doublePayment from "./scenario.example.json";
import multiFault from "./multi-fault-demo.json";

export interface BuiltinExample {
  key: string;
  label: string;
  description: string;
  scenario: unknown;
}

export const BUILTIN_EXAMPLES: BuiltinExample[] = [
  {
    key: "double-payment",
    label: "double-payment-demo",
    description: "Single fault: drop the payments response after it already succeeded, forcing a client retry and a second charge.",
    scenario: doublePayment,
  },
  {
    key: "multi-fault",
    label: "multi-fault-demo",
    description: "Two interventions: the same drop-after-success fault plus an unrelated decoy delay on inventory. Good for demonstrating reduction.",
    scenario: multiFault,
  },
];
