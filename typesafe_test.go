package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testClient() *client {
	return &client{
		apiKey: "test-key", model: "jev-latest",
		http: &http.Client{}, retries: 3, backoff: time.Millisecond,
		logf: func(string, ...any) {},
	}
}

// serve stands in for the API and records what it was sent.
func serve(t *testing.T, handler http.HandlerFunc) (*client, func() []byte) {
	t.Helper()
	var lastBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastBody, _ = io.ReadAll(r.Body)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	c := testClient()
	c.endpoint = srv.URL
	return c, func() []byte { return lastBody }
}

func TestEvaluateSendsAuthAndBody(t *testing.T) {
	var gotAuth, gotType string
	c, body := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotType = r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		fmt.Fprint(w, sample)
	})

	resp, err := c.evaluate(State{Diff: "some diff"}, defaultQuestions())
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotType != "application/json" {
		t.Errorf("Content-Type = %q", gotType)
	}
	if resp.Answers["verdict"].Choice != "request_changes" {
		t.Errorf("answers not decoded: %+v", resp.Answers)
	}

	var sent map[string]any
	if err := json.Unmarshal(body(), &sent); err != nil {
		t.Fatal(err)
	}
	if sent["model"] != "jev-latest" {
		t.Errorf("model = %v", sent["model"])
	}
	if _, ok := sent["state"]; !ok {
		t.Error("state missing from request")
	}
}

func TestEvaluateRetriesThenSucceeds(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, 529} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			c, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls < 3 {
					w.WriteHeader(status)
					return
				}
				fmt.Fprint(w, sample)
			})
			if _, err := c.evaluate(State{}, nil); err != nil {
				t.Fatal(err)
			}
			if calls != 3 {
				t.Errorf("calls = %d, want 3", calls)
			}
		})
	}
}

func TestEvaluateDoesNotRetryClientError(t *testing.T) {
	calls := 0
	c, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"error":"malformed question"}`)
	})

	_, err := c.evaluate(State{}, nil)
	if err == nil {
		t.Fatal("want an error")
	}
	if calls != 1 {
		t.Errorf("422 was retried %d times, want 1 call", calls)
	}
	if !strings.Contains(err.Error(), "malformed question") {
		t.Errorf("error should quote the body, got: %v", err)
	}
}

func TestEvaluateGivesUp(t *testing.T) {
	c, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(529)
	})
	c.retries = 2
	if _, err := c.evaluate(State{}, nil); err == nil || !strings.Contains(err.Error(), "gave up after 2") {
		t.Errorf("got %v", err)
	}
}

func TestEvaluateRejectsGarbageJSON(t *testing.T) {
	c, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not json")
	})
	if _, err := c.evaluate(State{}, nil); err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Errorf("got %v", err)
	}
}
