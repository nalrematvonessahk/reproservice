// orders is the demo SUT entry point for the checkout flow. It acts as its own
// deterministic "test driver" for the retry/attempt numbering described in
// docs/07-architecture.md ("для MVP номер попытки детерминированный и задаётся
// тестовым драйвером"): no jitter, a fixed short timeout, and at most one retry.
//
// orders calls payments and inventory through the proxy (not directly), tagging each
// call with the headers the proxy/controller use to build a Selector: X-Logical-Operation,
// X-Source, X-Run-Id, X-Attempt. If the proxy drops a successful payments response
// (the "double-payment" scenario), orders' retry re-sends /charge, and because the demo
// payments service does not deduplicate, a second payment row is created -- a real,
// reproducible bug, not a simulated one.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"reproservice/internal/httpx"
)

type checkoutRequest struct {
	OrderID          string `json:"order_id"`
	LogicalOperation string `json:"logical_operation"`
	RunID            string `json:"run_id"`
	AmountCents      int64  `json:"amount_cents"`
	SKU              string `json:"sku"`
}

type checkoutResponse struct {
	OrderID       string `json:"order_id"`
	Status        string `json:"status"`
	PaymentID     string `json:"payment_id,omitempty"`
	ReservationID string `json:"reservation_id,omitempty"`
	Attempts      int    `json:"payment_attempts"`
}

const maxPaymentAttempts = 2

func main() {
	dsn := os.Getenv("TESTAPP_DATABASE_URL")
	if dsn == "" {
		log.Fatal("TESTAPP_DATABASE_URL is required")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		log.Fatalf("orders: connect db: %v", err)
	}
	defer pool.Close()

	paymentsURL := envDefault("PAYMENTS_PROXY_URL", "http://localhost:9000/payments/charge")
	inventoryURL := envDefault("INVENTORY_PROXY_URL", "http://localhost:9000/inventory/reserve")
	client := &http.Client{Timeout: 3 * time.Second}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			httpx.WriteError(w, 503, "DB_UNAVAILABLE", err.Error(), "")
			return
		}
		httpx.WriteJSON(w, 200, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /checkout", func(w http.ResponseWriter, r *http.Request) {
		var req checkoutRequest
		if err := httpx.DecodeJSON(r, &req); err != nil || req.OrderID == "" || req.LogicalOperation == "" || req.RunID == "" {
			httpx.WriteError(w, 400, "INVALID_BODY", "order_id, logical_operation and run_id are required", "")
			return
		}
		ctx := r.Context()
		if _, err := pool.Exec(ctx, `INSERT INTO orders (order_id, status) VALUES ($1,'pending')
			ON CONFLICT (order_id) DO NOTHING`, req.OrderID); err != nil {
			httpx.WriteError(w, 500, "DB_ERROR", err.Error(), "")
			return
		}

		paymentID, attempts, err := chargeWithRetries(ctx, client, paymentsURL, req)
		if err != nil {
			pool.Exec(ctx, `UPDATE orders SET status='payment_failed' WHERE order_id=$1`, req.OrderID)
			httpx.WriteError(w, 502, "UPSTREAM_PAYMENTS_FAILED", err.Error(), "")
			return
		}

		resID, err := reserveOnce(ctx, client, inventoryURL, req)
		if err != nil {
			pool.Exec(ctx, `UPDATE orders SET status='inventory_failed' WHERE order_id=$1`, req.OrderID)
			httpx.WriteError(w, 502, "UPSTREAM_INVENTORY_FAILED", err.Error(), "")
			return
		}

		pool.Exec(ctx, `UPDATE orders SET status='completed' WHERE order_id=$1`, req.OrderID)
		httpx.WriteJSON(w, 200, checkoutResponse{
			OrderID:       req.OrderID,
			Status:        "completed",
			PaymentID:     paymentID,
			ReservationID: resID,
			Attempts:      attempts,
		})
	})

	addr := envDefault("LISTEN_ADDR", ":8081")
	log.Printf("orders listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

// chargeWithRetries performs the deterministic retry behaviour that makes the
// double-payment bug reproducible: no jitter, fixed attempt count, retries only on a
// transport-level failure (which is what a dropped response looks like to the caller).
func chargeWithRetries(ctx context.Context, client *http.Client, url string, req checkoutRequest) (paymentID string, attempts int, err error) {
	body, _ := json.Marshal(map[string]any{"order_id": req.OrderID, "amount_cents": req.AmountCents})
	for attempt := 1; attempt <= maxPaymentAttempts; attempt++ {
		attempts = attempt
		httpReq, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("X-Logical-Operation", req.LogicalOperation)
		httpReq.Header.Set("X-Source", "orders")
		httpReq.Header.Set("X-Run-Id", req.RunID)
		httpReq.Header.Set("X-Attempt", strconv.Itoa(attempt))

		resp, reqErr := client.Do(httpReq)
		if reqErr != nil {
			err = reqErr
			if ctx.Err() != nil {
				return "", attempts, ctx.Err() // the caller gave up (cancel/timeout): do not retry
			}
			continue // transport failure (e.g. dropped response) -> deterministic retry
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 500 {
			err = httpStatusErr(resp.StatusCode)
			continue
		}
		var out struct {
			PaymentID string `json:"payment_id"`
		}
		json.NewDecoder(resp.Body).Decode(&out)
		return out.PaymentID, attempts, nil
	}
	return "", attempts, err
}

func reserveOnce(ctx context.Context, client *http.Client, url string, req checkoutRequest) (string, error) {
	body, _ := json.Marshal(map[string]any{"order_id": req.OrderID, "sku": req.SKU})
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Logical-Operation", req.LogicalOperation)
	httpReq.Header.Set("X-Source", "orders")
	httpReq.Header.Set("X-Run-Id", req.RunID)
	httpReq.Header.Set("X-Attempt", "1")

	resp, err := client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", httpStatusErr(resp.StatusCode)
	}
	var out struct {
		ReservationID string `json:"reservation_id"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	return out.ReservationID, nil
}

type statusErr int

func (e statusErr) Error() string  { return "upstream returned status " + strconv.Itoa(int(e)) }
func httpStatusErr(code int) error { return statusErr(code) }

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
