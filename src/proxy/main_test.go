package main

import (
	"net/http"
	"testing"

	"reproservice/internal/schema"
)

func TestRedact_MasksSensitiveHeadersOnly(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer secret-token")
	h.Set("Cookie", "session=abc123")
	h.Set("X-Logical-Operation", "checkout-001")
	redact(h)
	if h.Get("Authorization") != "[redacted]" {
		t.Fatalf("expected Authorization redacted, got %q", h.Get("Authorization"))
	}
	if h.Get("Cookie") != "[redacted]" {
		t.Fatalf("expected Cookie redacted, got %q", h.Get("Cookie"))
	}
	if h.Get("X-Logical-Operation") != "checkout-001" {
		t.Fatalf("non-sensitive header must survive redaction, got %q", h.Get("X-Logical-Operation"))
	}
}

func TestParseRoutes_ValidSpec(t *testing.T) {
	routes, err := parseRoutes("payments=http://payments:8082,inventory=http://inventory:8083")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if routes["payments"].Host != "payments:8082" {
		t.Fatalf("expected payments host payments:8082, got %v", routes["payments"])
	}
	if routes["inventory"].Host != "inventory:8083" {
		t.Fatalf("expected inventory host inventory:8083, got %v", routes["inventory"])
	}
}

func TestParseRoutes_RejectsMalformedEntry(t *testing.T) {
	if _, err := parseRoutes("payments-http://payments:8082"); err == nil {
		t.Fatal("expected error for entry missing '='")
	}
}

func TestActiveRun_OnlyMatchesSelectorForActiveRun(t *testing.T) {
	ar := &activeRun{}
	sel := schema.Selector{LogicalOperation: "op", Source: "orders", Target: "payments", Method: "POST", NormalizedPath: "/charge", Attempt: 1}
	iv := schema.Intervention{ID: "f1", Selector: sel, Action: schema.Action{Type: schema.ActionDropResponseAfterSuccess}}

	if err := ar.activate("run-1", []schema.Intervention{iv}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	if _, ok := ar.lookup("run-1", sel); !ok {
		t.Fatal("expected selector to match for the active run")
	}
	if _, ok := ar.lookup("run-2", sel); ok {
		t.Fatal("must not match for a different run id")
	}
	if _, ok := ar.lookup("run-1", schema.Selector{LogicalOperation: "other"}); ok {
		t.Fatal("must not match an unrelated selector")
	}
}

func TestActiveRun_RejectsActivateWhileBusyWithAnotherRun(t *testing.T) {
	ar := &activeRun{}
	if err := ar.activate("run-1", nil); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if err := ar.activate("run-2", nil); err == nil {
		t.Fatal("expected error activating a second run while one is active")
	}
	// Re-activating the same run id (idempotent retry) must not error.
	if err := ar.activate("run-1", nil); err != nil {
		t.Fatalf("re-activating the same run should be allowed: %v", err)
	}
}

func TestActiveRun_DeactivateRejectsWrongRun(t *testing.T) {
	ar := &activeRun{}
	ar.activate("run-1", nil)
	if err := ar.deactivate("run-2"); err == nil {
		t.Fatal("expected error deactivating with the wrong run id")
	}
	if err := ar.deactivate("run-1"); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if _, ok := ar.lookup("run-1", schema.Selector{}); ok {
		t.Fatal("expected no active run after deactivate")
	}
}
