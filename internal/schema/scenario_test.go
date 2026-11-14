package schema

import "testing"

func validScenario() *Scenario {
	return &Scenario{
		SchemaVersion: SchemaVersion,
		ID:            "t1",
		Environment:   Environment{ComposeProfile: "orders-demo", SeedID: "seed-001"},
		InvariantID:   "payment_exactly_once",
		Budget:        Budget{Runs: 100, Seconds: 600},
		Interventions: []Intervention{
			{ID: "a", Selector: Selector{LogicalOperation: "checkout", Source: "orders", Target: "payments", Method: "POST", NormalizedPath: "/charge", Attempt: 1}, Action: Action{Type: ActionDropResponseAfterSuccess}},
		},
	}
}

func TestValidate_OK(t *testing.T) {
	if err := validScenario().Validate(); err != nil {
		t.Fatalf("expected valid scenario, got %v", err)
	}
}

func TestValidate_UnknownDependency(t *testing.T) {
	sc := validScenario()
	sc.Interventions[0].DependsOn = []string{"missing"}
	if err := sc.Validate(); err == nil {
		t.Fatal("expected error for unknown depends_on id")
	}
}

func TestValidate_DependencyCycle(t *testing.T) {
	sc := validScenario()
	sc.Interventions = append(sc.Interventions, Intervention{
		ID:        "b",
		Selector:  Selector{LogicalOperation: "checkout", Source: "orders", Target: "inventory", Method: "POST", NormalizedPath: "/reserve", Attempt: 1},
		Action:    Action{Type: ActionDelay, DelayMs: 100},
		DependsOn: []string{"a"},
	})
	sc.Interventions[0].DependsOn = []string{"b"}
	if err := sc.Validate(); err == nil {
		t.Fatal("expected dependency cycle error")
	}
}

func TestValidate_DuplicateInterventionID(t *testing.T) {
	sc := validScenario()
	sc.Interventions = append(sc.Interventions, sc.Interventions[0])
	if err := sc.Validate(); err == nil {
		t.Fatal("expected duplicate id error")
	}
}

func TestValidate_UnknownActionType(t *testing.T) {
	sc := validScenario()
	sc.Interventions[0].Action.Type = "delete_everything"
	if err := sc.Validate(); err == nil {
		t.Fatal("expected error for unknown action type")
	}
}

func TestSelectorKey_Deterministic(t *testing.T) {
	s := Selector{LogicalOperation: "checkout", Source: "orders", Target: "payments", Method: "POST", NormalizedPath: "/charge", Attempt: 1}
	if s.Key() != s.Key() {
		t.Fatal("selector key must be deterministic")
	}
	s2 := s
	s2.Attempt = 2
	if s.Key() == s2.Key() {
		t.Fatal("different attempt must produce a different key")
	}
}
