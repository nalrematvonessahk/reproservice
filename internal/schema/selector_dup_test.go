package schema

import "testing"

// Two interventions on the same selector are an ambiguous match (FR03): the proxy can
// apply only one, so the scenario is rejected before it can run.
func TestScenarioValidate_RejectsDuplicateSelector(t *testing.T) {
	sel := Selector{LogicalOperation: "op", Source: "orders", Target: "payments", Method: "POST", NormalizedPath: "/charge", Attempt: 1}
	mk := func(second Selector) *Scenario {
		return &Scenario{
			SchemaVersion: SchemaVersion, ID: "s", InvariantID: "payment_exactly_once",
			Environment: Environment{ComposeProfile: "p", SeedID: "seed"},
			Budget:      Budget{Runs: 10, Seconds: 60},
			Interventions: []Intervention{
				{ID: "a", Selector: sel, Action: Action{Type: ActionDropResponseAfterSuccess}},
				{ID: "b", Selector: second, Action: Action{Type: ActionDelay, DelayMs: 10}},
			},
		}
	}
	if err := mk(sel).Validate(); err == nil {
		t.Fatal("same selector on two interventions must be rejected")
	}
	other := sel
	other.Attempt = 2
	if err := mk(other).Validate(); err != nil {
		t.Fatalf("distinct selectors must stay valid: %v", err)
	}
}
