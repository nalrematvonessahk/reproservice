// controller is the API and run-lifecycle owner described in docs/07-architecture.md.
//
// NFR01 ("API слушает localhost") is enforced at the Docker Compose boundary, not by
// binding this process to 127.0.0.1: inside its own container, 127.0.0.1 refers only
// to the container itself, so binding there would also cut off the proxy container's
// event-ingestion calls over the compose network. Instead the controller binds all
// interfaces inside its container (LISTEN_ADDR default ":7000"), and
// deploy/docker-compose.yml publishes that port to the host as "127.0.0.1:7000:7000"
// -- reachable from the engineer's browser on localhost, not from outside the host,
// while other containers on the isolated compose network can still reach it by name.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"reproservice/src/controller/store"
	"reproservice/src/oracle"
	"reproservice/src/runner"
)

func main() {
	ctx := context.Background()

	metaDSN := mustEnv("METADATA_DATABASE_URL")
	testAppDSN := mustEnv("TESTAPP_DATABASE_URL")

	metaPool, err := pgxpool.New(ctx, metaDSN)
	if err != nil {
		log.Fatalf("controller: connect metadata db: %v", err)
	}
	defer metaPool.Close()

	testAppPool, err := pgxpool.New(ctx, testAppDSN)
	if err != nil {
		log.Fatalf("controller: connect testapp db: %v", err)
	}
	defer testAppPool.Close()

	healthURLs := splitNonEmpty(envDefault("HEALTH_URLS",
		"http://localhost:8081/healthz,http://localhost:8082/healthz,http://localhost:8083/healthz,http://localhost:9000/healthz"))
	seedPath := envDefault("SEED_SQL_PATH", "deploy/sql/seed.sql")

	rn, err := runner.New(testAppPool, seedPath, healthURLs)
	if err != nil {
		log.Fatalf("controller: init runner: %v", err)
	}
	or := oracle.New(testAppPool)
	st := store.New(metaPool)
	if err := ensureSchemaWithRetry(ctx, st); err != nil {
		log.Fatalf("controller: ensure metadata schema: %v", err)
	}
	px := NewProxyClient(envDefault("PROXY_ADMIN_URL", "http://localhost:9000"))
	sut := NewSUTClient(envDefault("ORDERS_URL", "http://localhost:8081"))

	orch := NewOrchestrator(st, rn, or, px, sut)
	api := NewAPI(st, orch)

	mux := http.NewServeMux()
	mux.Handle("/", api.Routes())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })

	addr := envDefault("LISTEN_ADDR", ":7000") // NFR01 is enforced by the compose port publish, see file header
	log.Printf("controller listening on %s (seed hash %s)", addr, rn.SeedHash())
	log.Fatal(http.ListenAndServe(addr, withLocalCORS(mux)))
}

// withLocalCORS allows the web UI's dev server (a different localhost port) to call
// this API from the browser. It only ever reflects localhost/127.0.0.1 origins --
// NFR01 restricts this API to the engineer's own machine, not the public internet.
func withLocalCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if strings.HasPrefix(origin, "http://localhost:") || strings.HasPrefix(origin, "http://127.0.0.1:") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,DELETE,OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ensureSchemaWithRetry tolerates the metadata DB still coming up (the compose
// healthcheck can pass on the Unix socket before TCP accepts connections), so the
// controller does not exit on a transient connection error at startup.
func ensureSchemaWithRetry(ctx context.Context, st *store.Store) error {
	var err error
	for attempt := 1; attempt <= 30; attempt++ {
		if err = st.EnsureSchema(ctx); err == nil {
			return nil
		}
		log.Printf("controller: metadata schema not ready (attempt %d/30): %v", attempt, err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return err
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("controller: required env var %s is not set", key)
	}
	return v
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func splitNonEmpty(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
