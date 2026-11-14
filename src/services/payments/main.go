// payments is a demo SUT service. It deliberately does NOT deduplicate charges by
// order_id: each call to /charge inserts a new "succeeded" payment row. This is the
// real, reproducible bug the "double-payment" scenario (config/scenario.example.json)
// is built to reveal: if the proxy drops a successful response (FR05) and the caller
// (orders) retries, payments double-charges. The oracle's payment_exactly_once check
// (src/oracle) catches this by counting succeeded rows per order_id.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"reproservice/internal/httpx"
)

type chargeRequest struct {
	OrderID     string `json:"order_id"`
	AmountCents int64  `json:"amount_cents"`
}

type chargeResponse struct {
	PaymentID string `json:"payment_id"`
	Status    string `json:"status"`
}

func main() {
	dsn := os.Getenv("TESTAPP_DATABASE_URL")
	if dsn == "" {
		log.Fatal("TESTAPP_DATABASE_URL is required")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		log.Fatalf("payments: connect db: %v", err)
	}
	defer pool.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			httpx.WriteError(w, 503, "DB_UNAVAILABLE", err.Error(), "")
			return
		}
		httpx.WriteJSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /charge", func(w http.ResponseWriter, r *http.Request) {
		var req chargeRequest
		if err := httpx.DecodeJSON(r, &req); err != nil || req.OrderID == "" {
			httpx.WriteError(w, 400, "INVALID_BODY", "order_id and amount_cents are required", "order_id")
			return
		}
		paymentID := req.OrderID + "-pay-" + time.Now().UTC().Format("150405.000000000")
		_, err := pool.Exec(r.Context(),
			`INSERT INTO payments (payment_id, order_id, status, amount_cents) VALUES ($1,$2,'succeeded',$3)`,
			paymentID, req.OrderID, req.AmountCents)
		if err != nil {
			httpx.WriteError(w, 500, "DB_ERROR", err.Error(), "")
			return
		}
		httpx.WriteJSON(w, 200, chargeResponse{PaymentID: paymentID, Status: "succeeded"})
	})

	addr := envDefault("LISTEN_ADDR", ":8082")
	log.Printf("payments listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
