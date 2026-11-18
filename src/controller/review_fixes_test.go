package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"reproservice/internal/schema"
)

// FR09: a cancel that arrives immediately after StartRun (i.e. right after the 202)
// must find the run's cancel function -- registration is synchronous, not racing the
// goroutine start.
func TestStartRun_CancelImmediatelyIsNotLost(t *testing.T) {
	w := newWorld()
	w.checkout = func(ctx context.Context, _ string, _ []string) error {
		<-ctx.Done()
		return ctx.Err()
	}
	run := w.addRun("r-early", schema.StateValidating, []string{"f1"})
	o := w.orch()
	if !o.TryAcquire() {
		t.Fatal("stand should be free")
	}
	o.StartRun(run, scenario(10, 60, iv("f1")))
	if !o.Cancel("r-early") { // no sleep: must be registered before StartRun returned
		t.Fatal("Cancel right after StartRun reported not_running; the cancel was lost")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !o.TryAcquire() {
		if time.Now().After(deadline) {
			t.Fatal("stand was never released after cancel")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st := w.state("r-early"); st != schema.StateCancelled {
		t.Fatalf("state=%s, want CANCELLED", st)
	}
}

// A proxy that does not return match counts (older/incompatible build) is an
// infrastructure error, never "every intervention matched 0 events" (INAPPLICABLE).
func TestProxyClient_Deactivate_MissingFiredIsError(t *testing.T) {
	for name, body := range map[string]string{
		"old proxy, no field": `{"status":"deactivated"}`,
		"null counts":         `{"status":"deactivated","fired":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(body))
			}))
			defer srv.Close()
			if got, err := NewProxyClient(srv.URL).Deactivate("r1"); err == nil {
				t.Fatalf("expected an error, got counts %v", got)
			}
		})
	}
	t.Run("empty but present counts are valid", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"status":"deactivated","fired":{}}`))
		}))
		defer srv.Close()
		got, err := NewProxyClient(srv.URL).Deactivate("r1")
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("got %v, %v", got, err)
		}
	})
	t.Run("counts are decoded", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"status":"deactivated","fired":{"a":1,"b":0}}`))
		}))
		defer srv.Close()
		got, err := NewProxyClient(srv.URL).Deactivate("r1")
		if err != nil || got["a"] != 1 || got["b"] != 0 {
			t.Fatalf("got %v, %v", got, err)
		}
	})
}
