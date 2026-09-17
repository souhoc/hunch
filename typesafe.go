package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const endpoint = "https://api.typesafe.ai/v1/systemone"

// TypeSafe charges for input tokens only; output tokens are free.
// https://docs.typesafe.ai/models — override with -price when the rate moves.
const defaultPricePerMTok = 0.042

type request struct {
	State     any                 `json:"state"`
	Model     string              `json:"model"`
	Questions map[string]Question `json:"questions"`
}

// Answer holds every answer shape; only the fields for its Type are set.
type Answer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Legend        map[string]string  `json:"legend,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
}

type response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type client struct {
	apiKey  string
	model   string
	http    *http.Client
	retries int
	logf    func(format string, a ...any)
}

func (c *client) evaluate(state any, questions map[string]Question) (*response, error) {
	body, err := json.Marshal(request{State: state, Model: c.model, Questions: questions})
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	backoff := time.Second
	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		if attempt > 0 {
			c.logf("retry %d/%d in %s (%v)", attempt, c.retries, backoff, lastErr)
			time.Sleep(backoff)
			backoff *= 2
		}

		resp, retryable, err := c.post(body)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !retryable {
			return nil, err
		}
	}
	return nil, fmt.Errorf("gave up after %d retries: %w", c.retries, lastErr)
}

func (c *client) post(body []byte) (*response, bool, error) {
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	httpResp, err := c.http.Do(req)
	if err != nil {
		return nil, true, err // network hiccup, worth a retry
	}
	defer httpResp.Body.Close()

	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, true, err
	}

	if httpResp.StatusCode != http.StatusOK {
		retryable := httpResp.StatusCode == http.StatusTooManyRequests || httpResp.StatusCode == 529
		return nil, retryable, fmt.Errorf("typesafe %s: %s", httpResp.Status, truncate(string(raw), 500))
	}

	var out response
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false, fmt.Errorf("decode response: %w", err)
	}
	return &out, false, nil
}
