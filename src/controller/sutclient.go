package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// SUTClient drives the demo application's checkout flow for a run. It stands in for
// the "test driver" mentioned in docs/07-architecture.md: deterministic, no load
// generation beyond this single logical operation (that's a Could/NFR04 concern, not
// required for the MVP record/replay/reduce loop).
type SUTClient struct {
	ordersURL string
	client    *http.Client
}

func NewSUTClient(ordersURL string) *SUTClient {
	return &SUTClient{ordersURL: ordersURL, client: &http.Client{}}
}

type CheckoutResult struct {
	OrderID       string `json:"order_id"`
	Status        string `json:"status"`
	PaymentID     string `json:"payment_id"`
	ReservationID string `json:"reservation_id"`
}

// Checkout runs one instance of the logical operation the scenario's interventions
// target. runID is passed through as X-Run-Id (via body, consumed by orders) so the
// proxy can scope fault matching to this run.
func (c *SUTClient) Checkout(ctx context.Context, orderID, logicalOperation, runID string) (*CheckoutResult, error) {
	body, _ := json.Marshal(map[string]any{
		"order_id":          orderID,
		"logical_operation": logicalOperation,
		"run_id":            runID,
		"amount_cents":      1999,
		"sku":               "sku-demo-1",
	})
	req, err := http.NewRequestWithContext(ctx, "POST", c.ordersURL+"/checkout", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("sutclient: build checkout request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sutclient: checkout request: %w", err)
	}
	defer resp.Body.Close()
	var out CheckoutResult
	json.NewDecoder(resp.Body).Decode(&out)
	out.OrderID = orderID
	if resp.StatusCode >= 500 {
		return &out, fmt.Errorf("sutclient: checkout returned %d", resp.StatusCode)
	}
	return &out, nil
}
