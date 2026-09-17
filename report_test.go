package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// sample is a response in the shape the API docs describe, one answer per type.
const sample = `{
  "model": "jev-latest",
  "answers": {
    "test_coverage": {"type": "noul", "noul": 0.12},
    "verdict": {
      "type": "choice",
      "choice": "request_changes",
      "probabilities": {"approve": 0.08, "comment": 0.2, "request_changes": 0.72},
      "confidence": 0.7
    },
    "correctness_risk": {
      "type": "score",
      "score": 1.6,
      "legend": {"0": "Safe", "1": "Low", "2": "Moderate", "3": "High"},
      "probabilities": {"0": 0.05, "1": 0.3, "2": 0.6, "3": 0.05},
      "confidence": 0.78
    }
  },
  "usage": {"input_tokens": 312, "output_tokens": 48}
}`

func TestRenderAnswers(t *testing.T) {
	var resp response
	if err := json.Unmarshal([]byte(sample), &resp); err != nil {
		t.Fatal(err)
	}

	if got := orderedIDs(resp.Answers)[0]; got != "verdict" {
		t.Errorf("verdict should come first, got %q", got)
	}

	questions := defaultQuestions()
	for id, want := range map[string]string{
		"test_coverage":    "no  (12% yes)",
		"verdict":          "request_changes  (70% confident)",
		"correctness_risk": "Moderate  1.6/3",
	} {
		if got := renderAnswer(id, questions[id], resp.Answers[id]); !strings.Contains(got, want) {
			t.Errorf("%s: want %q in:\n%s", id, want, got)
		}
	}
}

// goodness drives the colour, so a bad answer must not read as good.
func TestGoodness(t *testing.T) {
	var resp response
	if err := json.Unmarshal([]byte(sample), &resp); err != nil {
		t.Fatal(err)
	}
	q := defaultQuestions()

	cases := map[string]float64{
		"test_coverage":    0.12,      // yes is good, and it is mostly no
		"verdict":          0.0,       // request_changes
		"correctness_risk": 1 - 1.6/3, // low is good, and it is 1.6/3
	}
	for id, want := range cases {
		if got := goodness(q[id], resp.Answers[id]); got != want {
			t.Errorf("%s: goodness = %v, want %v", id, got, want)
		}
	}

	// a secret found is bad even though the answer is "yes"
	yes := 0.9
	if got := goodness(q["leaks_secrets"], Answer{Type: "noul", Noul: &yes}); got > 0.33 {
		t.Errorf("leaks_secrets yes should be bad, got %v", got)
	}
}

func TestParsePRURL(t *testing.T) {
	owner, repo, err := parsePRURL("https://github.com/cli/cli/pull/9000")
	if err != nil || owner != "cli" || repo != "cli" {
		t.Fatalf("got %q %q %v", owner, repo, err)
	}
	if _, _, err := parsePRURL("https://github.com/cli/cli/issues/1"); err == nil {
		t.Error("issue link should be rejected")
	}
}

// The good/good_choices fields are ours; the API must never see them.
func TestAPIQuestionsStripsDisplayFields(t *testing.T) {
	raw, err := json.Marshal(apiQuestions(defaultQuestions()))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"good"`, `"good_choices"`} {
		if strings.Contains(string(raw), field) {
			t.Errorf("%s leaked into the request body", field)
		}
	}
	if got := apiQuestions(defaultQuestions())["verdict"].Instructions; got == "" {
		t.Error("stripping dropped the instructions")
	}
}
