// Package typesafe is a client for the TypeSafe evaluation endpoint. See
// typesafe.md at the repo root for the request and answer shapes.
package typesafe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultEndpoint = "https://api.typesafe.ai/v1/systemone"

// TypeSafe charges for input tokens only; output tokens are free.
// https://docs.typesafe.ai/models — override with -price when the rate moves.
const DefaultPricePerMTok = 0.042

// Question is one typed question, exactly as the API accepts it.
// Criteria is: object (noul), map[string]string (choice), []string (score).
type Question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

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

// Response is the API reply: one Answer per question id.
type Response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type Client struct {
	APIKey   string
	Model    string
	Endpoint string // empty means defaultEndpoint; tests point it at httptest
	HTTP     *http.Client
	Retries  int
	Backoff  time.Duration // first retry delay, doubling after each attempt
	Logf     func(format string, a ...any)
}

// Evaluate asks every question about state, retrying 429/529 with backoff.
func (c *Client) Evaluate(state any, questions map[string]Question) (*Response, error) {
	body, err := json.Marshal(request{State: state, Model: c.Model, Questions: questions})
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	backoff := c.Backoff
	if backoff <= 0 {
		backoff = time.Second
	}
	var lastErr error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		if attempt > 0 {
			c.Logf("retry %d/%d in %s (%v)", attempt, c.Retries, backoff, lastErr)
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
	return nil, fmt.Errorf("gave up after %d retries: %w", c.Retries, lastErr)
}

func (c *Client) post(body []byte) (*Response, bool, error) {
	url := c.Endpoint
	if url == "" {
		url = defaultEndpoint
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	httpResp, err := c.HTTP.Do(req)
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
		return nil, retryable, fmt.Errorf("typesafe %s: %s", httpResp.Status, cut(string(raw), 500))
	}

	var out Response
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false, fmt.Errorf("decode response: %w", err)
	}
	return &out, false, nil
}

// cut keeps an error body readable when the server returns a page of HTML.
func cut(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + "..."
}
