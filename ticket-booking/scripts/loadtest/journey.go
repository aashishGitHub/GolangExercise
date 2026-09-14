package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Outcome is the terminal classification of one browse->queue->hold->
// order->ticket journey — one row of scripts/loadtest's CSV output.
type Outcome string

const (
	OutcomeTicketed     Outcome = "TICKETED"
	OutcomeSeatTaken    Outcome = "SEAT_TAKEN"
	OutcomeQueueTimeout Outcome = "QUEUE_TIMEOUT"
	OutcomeOrderFailed  Outcome = "ORDER_FAILED"
	OutcomeError        Outcome = "ERROR"
)

type Result struct {
	Seq          int
	StartedAt    time.Time
	Outcome      Outcome
	HoldMs       float64
	OrderMs      float64
	TotalMs      float64
	SeatOrdinal  int
}

type journeyRunner struct {
	client   *http.Client
	baseURL  string
	eventID  int64
	pickSeat func() int
	onHold   func(ordinal int, at time.Time) // notifies the WS lag observer
}

func (j *journeyRunner) run(ctx context.Context, seq int, token string) Result {
	start := time.Now()
	res := Result{Seq: seq, StartedAt: start}

	admissionToken, ok := j.joinQueue(ctx, token)
	if !ok {
		res.Outcome = OutcomeQueueTimeout
		res.TotalMs = msSince(start)
		return res
	}

	ordinal := j.pickSeat()
	res.SeatOrdinal = ordinal

	holdStart := time.Now()
	holdID, holdErr := j.acquireHold(ctx, token, admissionToken, ordinal)
	res.HoldMs = msSince(holdStart)
	if holdErr != nil {
		res.Outcome = holdErr.(outcomeErr).outcome
		res.TotalMs = msSince(start)
		return res
	}
	if j.onHold != nil {
		j.onHold(ordinal, time.Now())
	}

	orderStart := time.Now()
	orderID, err := j.createOrder(ctx, token, holdID)
	if err != nil {
		res.Outcome = OutcomeError
		res.TotalMs = msSince(start)
		return res
	}
	final := j.pollOrder(ctx, token, orderID)
	res.OrderMs = msSince(orderStart)
	res.Outcome = final
	res.TotalMs = msSince(start)
	return res
}

type outcomeErr struct {
	outcome Outcome
	err     error
}

func (e outcomeErr) Error() string { return string(e.outcome) + ": " + e.err.Error() }

// joinQueue polls up to a few times — enough for the local AIMD loop
// (1 tick/sec) to plausibly advance the cursor past this arrival without
// the load test hanging forever on a queue that's deliberately slow.
func (j *journeyRunner) joinQueue(ctx context.Context, token string) (admissionToken string, ok bool) {
	for attempt := 0; attempt < 5; attempt++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
			fmt.Sprintf("%s/api/v1/events/%d/queue", j.baseURL, j.eventID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := j.client.Do(req)
		if err != nil {
			return "", false
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var out struct {
				AdmissionToken string `json:"admissionToken"`
			}
			_ = json.Unmarshal(body, &out)
			return out.AdmissionToken, true
		}
		var pending struct {
			PollAfterMs int `json:"pollAfterMs"`
		}
		_ = json.Unmarshal(body, &pending)
		wait := time.Duration(pending.PollAfterMs) * time.Millisecond
		if wait <= 0 {
			wait = 200 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return "", false
		case <-time.After(wait):
		}
	}
	return "", false
}

func (j *journeyRunner) acquireHold(ctx context.Context, token, admissionToken string, ordinal int) (holdID string, err error) {
	body, _ := json.Marshal(map[string]any{"seatOrdinals": []int{ordinal}})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/api/v1/events/%d/holds", j.baseURL, j.eventID), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Admission-Token", admissionToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := j.client.Do(req)
	if err != nil {
		return "", outcomeErr{OutcomeError, err}
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusConflict {
		return "", outcomeErr{OutcomeSeatTaken, fmt.Errorf("409")}
	}
	if resp.StatusCode != http.StatusCreated {
		return "", outcomeErr{OutcomeError, fmt.Errorf("status %d: %s", resp.StatusCode, respBody)}
	}
	var out struct {
		HoldID string `json:"holdId"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", outcomeErr{OutcomeError, err}
	}
	return out.HoldID, nil
}

func (j *journeyRunner) createOrder(ctx context.Context, token, holdID string) (string, error) {
	body, _ := json.Marshal(map[string]string{"holdId": holdID})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		j.baseURL+"/api/v1/orders", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := j.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("create order: status %d: %s", resp.StatusCode, respBody)
	}
	var out struct {
		OrderID string `json:"orderId"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", err
	}
	return out.OrderID, nil
}

func (j *journeyRunner) pollOrder(ctx context.Context, token, orderID string) Outcome {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
			j.baseURL+"/api/v1/orders/"+orderID, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := j.client.Do(req)
		if err != nil {
			return OutcomeError
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var out struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(body, &out)
		switch out.Status {
		case "TICKETED":
			return OutcomeTicketed
		case "FAILED", "COMPENSATED":
			return OutcomeOrderFailed
		}
		time.Sleep(150 * time.Millisecond)
	}
	return OutcomeError
}

func msSince(t time.Time) float64 { return float64(time.Since(t).Microseconds()) / 1000.0 }
