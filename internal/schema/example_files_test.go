package schema

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExampleScenarioFiles_ParseAndValidate guards against drift between the schema
// code and the example scenario files actually shipped in config/ -- if someone edits
// one without the other, this test (not just the live stand) should catch it.
func TestExampleScenarioFiles_ParseAndValidate(t *testing.T) {
	files := []string{
		filepath.FromSlash("../../config/scenario.example.json"),
		filepath.FromSlash("../../config/scenarios/multi-fault-demo.json"),
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if _, err := ParseScenario(b); err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
	}
}
