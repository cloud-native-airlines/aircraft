// Package adsb is a small client for delivering position reports to the ADS-B
// ingestion API. Delivery uses a bounded deadline and a limited number of
// retries; a failure is returned (and logged by the caller) but never changes
// the flight's physical state.
package adsb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Report matches the ADS-B POST /api/v1/reports contract.
type Report struct {
	ReportID       string  `json:"report_id"`
	RunID          string  `json:"run_id"`
	TickID         int64   `json:"tick_id"`
	AircraftID     string  `json:"aircraft_id"`
	FlightID       string  `json:"flight_id"`
	SimulatedAt    string  `json:"simulated_at"`
	Latitude       float64 `json:"latitude"`
	Longitude      float64 `json:"longitude"`
	AltitudeM      float64 `json:"altitude_m"`
	GroundSpeedMPS float64 `json:"ground_speed_mps"`
	HeadingDeg     float64 `json:"heading_deg"`
	Status         string  `json:"status"`
}

type Client struct {
	baseURL    string
	http       *http.Client
	maxRetries int
}

// New returns a client. timeout bounds each attempt; maxRetries is the number of
// additional attempts after the first.
func New(baseURL string, timeout time.Duration, maxRetries int) *Client {
	return &Client{
		baseURL:    baseURL,
		http:       &http.Client{Timeout: timeout},
		maxRetries: maxRetries,
	}
}

// Send delivers a report, retrying transient failures. Retries reuse the same
// report_id and payload, which ADS-B treats idempotently.
func (c *Client) Send(ctx context.Context, r Report) error {
	body, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	url := c.baseURL + "/api/v1/reports"

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 200 * time.Millisecond):
			}
		}
		lastErr = c.attempt(ctx, url, body)
		if lastErr == nil {
			return nil
		}
		if permanent(lastErr) {
			return lastErr
		}
	}
	return lastErr
}

func (c *Client) attempt(ctx context.Context, url string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err // transient (network); will retry
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); resp.Body.Close() }()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode >= 500:
		return &httpError{status: resp.StatusCode, perm: false}
	default: // 4xx — a bad/conflicting request won't succeed on retry
		return &httpError{status: resp.StatusCode, perm: true}
	}
}

type httpError struct {
	status int
	perm   bool
}

func (e *httpError) Error() string { return fmt.Sprintf("adsb responded %d", e.status) }

func permanent(err error) bool {
	he, ok := err.(*httpError)
	return ok && he.perm
}
