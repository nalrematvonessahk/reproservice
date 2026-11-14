// inventory is a demo SUT service used by the checkout flow in src/services/orders.
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

type reserveRequest struct {
	OrderID string `json:"order_id"`
	SKU     string `json:"sku"`
}

type reserveResponse struct {
	ReservationID string `json:"reservation_id"`
	Status        string `json:"status"`
}

func main() {
	dsn := os.Getenv("TESTAPP_DATABASE_URL")
	if dsn == "" {
		log.Fatal("TESTAPP_DATABASE_URL is required")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		log.Fatalf("inventory: connect db: %v", err)
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
	mux.HandleFunc("POST /reserve", func(w http.ResponseWriter, r *http.Request) {
		var req reserveRequest
		if err := httpx.DecodeJSON(r, &req); err != nil || req.OrderID == "" || req.SKU == "" {
			httpx.WriteError(w, 400, "INVALID_BODY", "order_id and sku are required", "order_id")
			return
		}
		resID := req.OrderID + "-res-" + time.Now().UTC().Format("150405.000000000")
		_, err := pool.Exec(r.Context(),
			`INSERT INTO inventory_reservations (reservation_id, order_id, sku, status) VALUES ($1,$2,$3,'reserved')`,
			resID, req.OrderID, req.SKU)
		if err != nil {
			httpx.WriteError(w, 500, "DB_ERROR", err.Error(), "")
			return
		}
		httpx.WriteJSON(w, 200, reserveResponse{ReservationID: resID, Status: "reserved"})
	})

	addr := envDefault("LISTEN_ADDR", ":8083")
	log.Printf("inventory listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
