// Package schema defines the shared data contract described in docs/07-architecture.md:
// scenario/selector/event/run shapes used by the proxy, controller, runner, oracle and reducer.
package schema

import (
	"encoding/json"
	"fmt"
)

const SchemaVersion = 1

// Selector is the stable replay key for an HTTP interaction, per ADR-002.
// A random trace ID is intentionally not part of this key.
type Selector struct {
	LogicalOperation string `json:"logical_operation"`
	Source           string `json:"source"`
	Target           string `json:"target"`
	Method           string `json:"method"`
	NormalizedPath   string `json:"normalized_path"`
	Attempt          int    `json:"attempt"`
}

func (s Selector) Key() string {
	return fmt.Sprintf("%s|%s|%s|%s|%s|%d", s.LogicalOperation, s.Source, s.Target, s.Method, s.NormalizedPath, s.Attempt)
}

func (s Selector) Validate() error {
	if s.LogicalOperation == "" || s.Source == "" || s.Target == "" || s.Method == "" || s.NormalizedPath == "" {
		return fmt.Errorf("selector missing required field: %+v", s)
	}
	if s.Attempt < 1 {
		return fmt.Errorf("selector attempt must be >= 1: %+v", s)
	}
	return nil
}

// ActionType enumerates the fault actions the proxy can apply. Keep this list closed:
// the UI/API must never accept an arbitrary shell/SQL payload (ADR-004).
type ActionType string

const (
	ActionDelay                 ActionType = "delay"
	ActionHTTPError             ActionType = "http_error"
	ActionDropResponseAfterSuccess ActionType = "drop_response_after_success"
)

type Action struct {
	Type       ActionType `json:"type"`
	DelayMs    int        `json:"delay_ms,omitempty"`
	StatusCode int        `json:"status_code,omitempty"`
}

func (a Action) Validate() error {
	switch a.Type {
	case ActionDelay:
		if a.DelayMs <= 0 {
			return fmt.Errorf("delay action requires delay_ms > 0")
		}
	case ActionHTTPError:
		if a.StatusCode < 400 || a.StatusCode > 599 {
			return fmt.Errorf("http_error action requires status_code in [400,599]")
		}
	case ActionDropResponseAfterSuccess:
		// no extra fields required
	default:
		return fmt.Errorf("unknown action type %q", a.Type)
	}
	return nil
}

type Intervention struct {
	ID        string   `json:"id"`
	Selector  Selector `json:"selector"`
	Action    Action   `json:"action"`
	DependsOn []string `json:"depends_on"`
}

func (iv Intervention) Validate() error {
	if iv.ID == "" {
		return fmt.Errorf("intervention missing id")
	}
	if err := iv.Selector.Validate(); err != nil {
		return fmt.Errorf("intervention %s: %w", iv.ID, err)
	}
	if err := iv.Action.Validate(); err != nil {
		return fmt.Errorf("intervention %s: %w", iv.ID, err)
	}
	return nil
}

type Environment struct {
	ComposeProfile string            `json:"compose_profile"`
	ImageDigests   map[string]string `json:"image_digests"`
	SeedID         string            `json:"seed_id"`
	SeedHash       string            `json:"seed_hash"`
}

type Budget struct {
	Runs    int `json:"runs"`
	Seconds int `json:"seconds"`
}

type Scenario struct {
	SchemaVersion int            `json:"schema_version"`
	ID            string         `json:"id"`
	Kind          string         `json:"kind,omitempty"`
	Environment   Environment    `json:"environment"`
	InvariantID   string         `json:"invariant_id"`
	Budget        Budget         `json:"budget"`
	Interventions []Intervention `json:"interventions"`
}

// Validate rejects a scenario before it may enter VALIDATING -> RESETTING, per FR01/NFR01.
// It checks structural validity and the dependency DAG (FR13); it does not check the
// environment actually exists on disk -- that is the runner's job during RESETTING.
func (sc *Scenario) Validate() error {
	if sc.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", sc.SchemaVersion)
	}
	if sc.ID == "" {
		return fmt.Errorf("scenario missing id")
	}
	if sc.Environment.ComposeProfile == "" || sc.Environment.SeedID == "" {
		return fmt.Errorf("scenario environment missing compose_profile or seed_id")
	}
	if sc.InvariantID == "" {
		return fmt.Errorf("scenario missing invariant_id")
	}
	if sc.Budget.Runs <= 0 || sc.Budget.Seconds <= 0 {
		return fmt.Errorf("scenario budget must be positive")
	}
	if len(sc.Interventions) == 0 {
		return fmt.Errorf("scenario has no interventions")
	}
	ids := map[string]bool{}
	selectors := map[string]string{}
	for _, iv := range sc.Interventions {
		if err := iv.Validate(); err != nil {
			return err
		}
		if ids[iv.ID] {
			return fmt.Errorf("duplicate intervention id %q", iv.ID)
		}
		ids[iv.ID] = true
		// Two interventions on the same selector would be an ambiguous match (FR03):
		// the proxy can apply only one, so the other could never be observed to fire.
		if other, dup := selectors[iv.Selector.Key()]; dup {
			return fmt.Errorf("interventions %q and %q share the same selector (ambiguous match)", other, iv.ID)
		}
		selectors[iv.Selector.Key()] = iv.ID
	}
	for _, iv := range sc.Interventions {
		for _, dep := range iv.DependsOn {
			if !ids[dep] {
				return fmt.Errorf("intervention %s depends_on unknown id %q", iv.ID, dep)
			}
		}
	}
	if cyc := findCycle(sc.Interventions); cyc != "" {
		return fmt.Errorf("dependency cycle detected involving intervention %q", cyc)
	}
	return nil
}

func findCycle(ivs []Intervention) string {
	deps := map[string][]string{}
	for _, iv := range ivs {
		deps[iv.ID] = iv.DependsOn
	}
	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	state := map[string]int{}
	var stack []string
	var visit func(id string) string
	visit = func(id string) string {
		switch state[id] {
		case done:
			return ""
		case visiting:
			return id
		}
		state[id] = visiting
		stack = append(stack, id)
		for _, dep := range deps[id] {
			if cyc := visit(dep); cyc != "" {
				return cyc
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = done
		return ""
	}
	for _, iv := range ivs {
		if cyc := visit(iv.ID); cyc != "" {
			return cyc
		}
	}
	return ""
}

func ParseScenario(b []byte) (*Scenario, error) {
	var sc Scenario
	if err := json.Unmarshal(b, &sc); err != nil {
		return nil, fmt.Errorf("invalid scenario JSON: %w", err)
	}
	if err := sc.Validate(); err != nil {
		return nil, err
	}
	return &sc, nil
}
