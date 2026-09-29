package review

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/souhoc/hunch/v2/internal/github"
	"github.com/souhoc/hunch/v2/internal/typesafe"
)

// sample is a response in the shape the API docs describe, one answer per type.
const sample = `{
  "model": "jev-latest",
  "answers": {
    "description_matches_diff": {"type": "noul", "noul": 0.12},
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
	var resp typesafe.Response
	if err := json.Unmarshal([]byte(sample), &resp); err != nil {
		t.Fatal(err)
	}

	if got := orderedIDs(resp.Answers)[0]; got != "verdict" {
		t.Errorf("verdict should come first, got %q", got)
	}

	questions := Default()
	for id, want := range map[string]string{
		"description_matches_diff": "no  (12% yes)",
		"verdict":                  "request_changes  (70% confident)",
		"correctness_risk":         "Moderate  1.6/3",
	} {
		if got := renderAnswer(id, questions[id], resp.Answers[id]); !strings.Contains(got, want) {
			t.Errorf("%s: want %q in:\n%s", id, want, got)
		}
	}
}

// goodness drives the colour, so a bad answer must not read as good.
func TestGoodness(t *testing.T) {
	var resp typesafe.Response
	if err := json.Unmarshal([]byte(sample), &resp); err != nil {
		t.Fatal(err)
	}
	q := Default()

	cases := map[string]float64{
		"description_matches_diff": 0.12,      // yes is good, and it is mostly no
		"verdict":                  0.0,       // request_changes
		"correctness_risk":         1 - 1.6/3, // low is good, and it is 1.6/3
	}
	for id, want := range cases {
		if got := Goodness(q[id], resp.Answers[id]); got != want {
			t.Errorf("%s: goodness = %v, want %v", id, got, want)
		}
	}

	// a secret found is bad even though the answer is "yes"
	yes := 0.9
	if got := Goodness(q["leaks_secrets"], typesafe.Answer{Type: "noul", Noul: &yes}); got > 0.33 {
		t.Errorf("leaks_secrets yes should be bad, got %v", got)
	}
}

// The good/good_choices/blocks fields are ours; the API must never see them.
func TestAPIQuestionsStripsDisplayFields(t *testing.T) {
	raw, err := json.Marshal(APIQuestions(Default()))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"good"`, `"good_choices"`, `"blocks"`} {
		if strings.Contains(string(raw), field) {
			t.Errorf("%s leaked into the request body", field)
		}
	}
	if got := APIQuestions(Default())["verdict"].Instructions; got == "" {
		t.Error("stripping dropped the instructions")
	}
}

func TestMoney(t *testing.T) {
	for usd, want := range map[float64]string{
		0.000238: "$0.00024", // sub-cent must not collapse to $0.00
		0:        "$0.00000",
		1.5:      "$1.50",
	} {
		if got := money(usd); got != want {
			t.Errorf("money(%v) = %s, want %s", usd, got, want)
		}
	}
}

func TestNextStep(t *testing.T) {
	pr := github.PR{Number: 42, Owner: "o", Repo: "r"}
	q := Default()

	for name, tc := range map[string]struct{ choice, want string }{
		"high": {"high", "/code-review high 42"},
		"max":  {"max", "/code-review max 42"},
		"skip": {"skip", "no code review needed"},
	} {
		t.Run(name, func(t *testing.T) {
			got := nextStep(pr, q, map[string]typesafe.Answer{
				"code_review_effort": {Type: "choice", Choice: tc.choice},
			})
			if !strings.Contains(got, tc.want) {
				t.Errorf("got %q, want %q in it", got, tc.want)
			}
		})
	}

	// A custom rubric without the criterion gets no next-step line.
	if got := nextStep(pr, q, map[string]typesafe.Answer{}); got != "" {
		t.Errorf("want empty, got %q", got)
	}
}

func TestReport(t *testing.T) {
	var resp typesafe.Response
	if err := json.Unmarshal([]byte(sample), &resp); err != nil {
		t.Fatal(err)
	}
	resp.Answers["code_review_effort"] = typesafe.Answer{Type: "choice", Choice: "high"}

	var buf bytes.Buffer
	pr := github.PR{Number: 42, Title: "a title", URL: "https://example.test/pull/42",
		ChangedFiles: 1, Additions: 7, Deletions: 2, BaseRefName: "main", Owner: "o", Repo: "r"}
	Report(&buf, pr, "CLAUDE.md", Default(), &resp, 0.042)

	out := buf.String()
	for _, want := range []string{
		"#42  a title",
		"1 file  +7  -2  onto main", // singular
		"guidelines from CLAUDE.md",
		"/code-review high 42",
		"312 in / 48 out tokens",
		"$0.00001", // 312 tokens at $0.042/Mtok
		"(output free)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q\n%s", want, out)
		}
	}
}

func TestLoadQuestions(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	os.WriteFile(good, []byte(`{"q":{"type":"noul","instructions":"ok?"}}`), 0o644)
	q, err := Load(good)
	if err != nil || q["q"].Instructions != "ok?" {
		t.Fatalf("got %v, %v", q, err)
	}

	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte(`{`), 0o644)
	if _, err := Load(bad); err == nil {
		t.Error("malformed JSON accepted")
	}

	empty := filepath.Join(dir, "empty.json")
	os.WriteFile(empty, []byte(`{}`), 0o644)
	if _, err := Load(empty); err == nil || !strings.Contains(err.Error(), "no questions") {
		t.Errorf("got %v", err)
	}

	if _, err := Load(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("missing file accepted")
	}
}

// A confident secret or weakened control has to beat an approving verdict, not
// sit below it in the list.
func TestBlockers(t *testing.T) {
	q := Default()
	yes, no := 0.9, 0.05

	for name, tc := range map[string]struct {
		answers map[string]typesafe.Answer
		want    []string
	}{
		"clean":         {map[string]typesafe.Answer{"leaks_secrets": {Type: "noul", Noul: &no}}, nil},
		"one":           {map[string]typesafe.Answer{"leaks_secrets": {Type: "noul", Noul: &yes}}, []string{"leaks_secrets"}},
		"both":          {map[string]typesafe.Answer{"leaks_secrets": {Type: "noul", Noul: &yes}, "weakens_security": {Type: "noul", Noul: &yes}}, []string{"leaks_secrets", "weakens_security"}},
		"not a blocker": {map[string]typesafe.Answer{"in_scope": {Type: "noul", Noul: &no}}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			got := blockers(q, tc.answers)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("blockers = %v, want %v", got, tc.want)
			}
		})
	}
}

// The banner must never contradict the colour: blocking and red share redAt.
func TestBlockAtRedBoundary(t *testing.T) {
	q := map[string]Question{"x": {Type: "noul", Good: GoodNo, Blocks: true}}
	for _, tc := range []struct {
		noul    float64
		blocked bool
	}{
		{0.67, true},  // goodness 0.33, exactly red
		{0.66, false}, // goodness 0.34, amber
	} {
		p := tc.noul
		a := map[string]typesafe.Answer{"x": {Type: "noul", Noul: &p}}
		if got := len(blockers(q, a)) > 0; got != tc.blocked {
			t.Errorf("noul %.2f: blocked=%v, want %v", tc.noul, got, tc.blocked)
		}
		if tc.blocked != (colorOf(Goodness(q["x"], a["x"])) == red) {
			t.Errorf("noul %.2f: blocking and red disagree", tc.noul)
		}
	}
}

// The banner and the next-step line must agree. A committed secret in a diff the
// model rates "skip" is the case where they used to contradict each other: the
// report opened with a block and closed with a green all-clear.
func TestReportBlocked(t *testing.T) {
	var resp typesafe.Response
	if err := json.Unmarshal([]byte(sample), &resp); err != nil {
		t.Fatal(err)
	}
	yes := 0.95
	resp.Answers["leaks_secrets"] = typesafe.Answer{Type: "noul", Noul: &yes}
	resp.Answers["code_review_effort"] = typesafe.Answer{Type: "choice", Choice: "skip"}

	var buf bytes.Buffer
	pr := github.PR{Number: 42, Title: "a title", BaseRefName: "main", Owner: "o", Repo: "r"}
	Report(&buf, pr, "", Default(), &resp, 0.042)
	out := buf.String()

	for _, want := range []string{
		"⛔ approval blocked by leaks_secrets", // the banner is wired into report()
		"/code-review high 42",                // "skip" is overridden, not obeyed
		"leaks_secrets blocks approval",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("blocked report missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "no code review needed") {
		t.Errorf("blocked report still prints the all-clear\n%s", out)
	}
}

// ...and an unblocked "skip" still gets it.
func TestNextStepSkipUnblocked(t *testing.T) {
	no := 0.02
	got := nextStep(github.PR{Number: 7}, Default(), map[string]typesafe.Answer{
		"code_review_effort": {Type: "choice", Choice: "skip"},
		"leaks_secrets":      {Type: "noul", Noul: &no},
	})
	if !strings.Contains(got, "no code review needed") {
		t.Errorf("clean skip should say so, got %q", got)
	}
}

// A criterion with no polarity renders amber whatever it answers, so every
// default criterion must say which answer is the good one.
func TestDefaultsHavePolarity(t *testing.T) {
	for name, rubric := range map[string]map[string]Question{"pr": Default(), "comment": DefaultComment()} {
		for id, q := range rubric {
			if q.Good == "" && len(q.GoodChoices) == 0 {
				t.Errorf("%s rubric: %s has no polarity", name, id)
			}
		}
	}
}

func TestCommentReport(t *testing.T) {
	no := 0.1
	resp := typesafe.Response{
		Model: "jev-latest",
		Answers: map[string]typesafe.Answer{
			"inline_accurate": {Type: "noul", Noul: &no},
		},
	}
	resp.Usage.InputTokens = 100

	var buf bytes.Buffer
	CommentReport(&buf, 3, DefaultComment(), &resp, 0.042)
	out := buf.String()

	for _, want := range []string{"3 lines from stdin", "inline_accurate", "100 in / 0 out tokens"} {
		if !strings.Contains(out, want) {
			t.Errorf("comment report missing %q\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"approval blocked", "/code-review"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("comment report has PR-only %q\n%s", unwanted, out)
		}
	}
}
