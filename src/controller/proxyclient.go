package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"reproservice/internal/schema"
)

// ProxyClient talks to the proxy's admin API to scope fault injection to the
// currently active run (docs/07-architecture.md: one run occupies the stand at a time).
type ProxyClient struct {
	baseURL string
	client  *http.Client
}

func NewProxyClient(baseURL string) *ProxyClient {
	return &ProxyClient{baseURL: strings.TrimRight(baseURL, "/"), client: &http.Client{Timeout: 3 * time.Second}}
}

func (p *ProxyClient) Activate(runID string, interventions []schema.Intervention) error {
	body, _ := json.Marshal(map[string]any{"interventions": interventions})
	req, _ := http.NewRequest("POST", fmt.Sprintf("%s/admin/runs/%s/activate", p.baseURL, runID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("proxyclient: activate: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 409 {
		return fmt.Errorf("proxyclient: stand busy with another run")
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("proxyclient: activate returned %d", resp.StatusCode)
	}
	return nil
}

// Deactivate ends the run's fault scope and returns how many times each activated
// intervention's selector matched (see proxy activeRun.finish). Callers treat != 1 as
// INAPPLICABLE (FR03).
func (p *ProxyClient) Deactivate(runID string) (map[string]int, error) {
	req, _ := http.NewRequest("DELETE", fmt.Sprintf("%s/admin/runs/%s/activate", p.baseURL, runID), nil)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("proxyclient: deactivate: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("proxyclient: deactivate returned %d", resp.StatusCode)
	}
	var out struct {
		Fired map[string]int `json:"fired"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("proxyclient: decode deactivate response: %w", err)
	}
	if out.Fired == nil {
		out.Fired = map[string]int{}
	}
	return out.Fired, nil
}
