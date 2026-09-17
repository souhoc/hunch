package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAUC(t *testing.T) {
	cases := []struct {
		name   string
		scores []float64
		labels []bool
		want   float64
	}{
		{"perfect separation", []float64{1, 2, 3, 4}, []bool{false, false, true, true}, 1.0},
		{"reversed", []float64{1, 2, 3, 4}, []bool{true, true, false, false}, 0.0},
		{"all tied", []float64{5, 5, 5, 5}, []bool{true, true, false, false}, 0.5},
		{"mixed ties", []float64{1, 2, 2, 3}, []bool{false, true, false, true}, 0.875},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := auc(c.scores, c.labels)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("auc = %v, want %v", got, c.want)
			}
		})
	}

	if _, err := auc([]float64{1, 2}, []bool{true}); err == nil {
		t.Error("want a length-mismatch error")
	}
	if _, err := auc([]float64{1, 2}, []bool{true, true}); err == nil {
		t.Error("want an error when a label class is empty")
	}
}

func TestAnswered(t *testing.T) {
	n, s := 0.5, 1.0
	cases := []struct {
		name string
		a    Answer
		want bool
	}{
		{"noul with value", Answer{Type: "noul", Noul: &n}, true},
		{"noul nil", Answer{Type: "noul"}, false},
		{"score with value", Answer{Type: "score", Score: &s}, true},
		{"score nil", Answer{Type: "score"}, false},
		{"choice set", Answer{Type: "choice", Choice: "approve"}, true},
		{"choice empty", Answer{Type: "choice"}, false},
		{"unknown type", Answer{Type: "mystery"}, false},
	}
	for _, c := range cases {
		if got := answered(c.a); got != c.want {
			t.Errorf("%s: answered = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestLoadCorpus(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	valid := write("valid.json", `[
		{"url": "https://github.com/o/r/pull/1", "review_decision": "approved", "reason": "fine"},
		{"url": "https://github.com/o/r/pull/2", "review_decision": "changes_requested", "reason": "nope"}
	]`)
	entries, err := loadCorpus(valid)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].ReviewDecision != "approved" {
		t.Errorf("bad parse: %+v", entries)
	}

	if _, err := loadCorpus(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("want error on missing file")
	}
	if _, err := loadCorpus(write("malformed.json", `not json`)); err == nil {
		t.Error("want error on malformed JSON")
	}
	if _, err := loadCorpus(write("empty.json", `[]`)); err == nil {
		t.Error("want error on empty corpus")
	}
	if _, err := loadCorpus(write("nourl.json", `[{"review_decision": "approved"}]`)); err == nil {
		t.Error("want error on missing url")
	}

	_, err = loadCorpus(write("bad.json", `[{"url": "https://github.com/o/r/pull/1", "review_decision": "maybe"}]`))
	if err == nil || !strings.Contains(err.Error(), "maybe") {
		t.Errorf("got %v, want an error naming the bad value", err)
	}
}

// A 4-entry corpus, one of which fails at gh, exercises the full loop: a
// skipped entry must not abort the run, and a cleanly-separated criterion
// must score a perfect AUC using the goodness the normal report renders with.
func TestRunEvalAndAUCTable(t *testing.T) {
	fakeGH(t, `case "$*" in
	*"pull/4"*"--json"*) exit 1 ;;
	*"pr view"*) echo '{"number":1,"title":"t","body":"b","baseRefName":"main","headRefOid":"sha","additions":1,"deletions":1,"changedFiles":1}' ;;
	*"pr diff"*) echo "diff --git a/x b/x" ;;
	*) exit 1 ;;
	esac`)

	calls := 0
	c, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		score := 0.0 // Safe: goodness 1.0 for correctness_risk (Good: low)
		if calls == 3 {
			score = 3.0 // High: goodness 0.0
		}
		fmt.Fprintf(w, `{"model":"jev-latest","answers":{"correctness_risk":{
			"type":"score","score":%v,
			"legend":{"0":"Safe","1":"Low","2":"Moderate","3":"High"}
		}}}`, score)
	})

	corpus := []CorpusEntry{
		{URL: "https://github.com/o/r/pull/1", ReviewDecision: "approved"},
		{URL: "https://github.com/o/r/pull/2", ReviewDecision: "approved"},
		{URL: "https://github.com/o/r/pull/3", ReviewDecision: "changes_requested"},
		{URL: "https://github.com/o/r/pull/4", ReviewDecision: "changes_requested"}, // fails at gh
	}
	o := &options{maxDiff: 1000, maxDoc: 1000, docNames: "CLAUDE.md,README.md"}
	logf := func(string, ...any) {}

	results := runEval(c, defaultQuestions(), corpus, o, logf)
	if len(results) != 4 {
		t.Fatalf("got %d results, want 4", len(results))
	}
	for _, r := range results {
		wantErr := r.URL == "https://github.com/o/r/pull/4"
		if (r.Err != "") != wantErr {
			t.Errorf("%s: Err = %q, want error=%v", r.URL, r.Err, wantErr)
		}
	}
	if !anySucceeded(results) {
		t.Fatal("want at least one success")
	}

	table := aucTable(results, defaultQuestions())
	var row *criterionAUC
	for i := range table {
		if table[i].ID == "correctness_risk" {
			row = &table[i]
		}
	}
	if row == nil || !row.Valid || row.AUC != 1.0 || row.N != 3 {
		t.Errorf("correctness_risk row = %+v", row)
	}
}

func TestPrintAUCTable(t *testing.T) {
	var buf strings.Builder
	printAUCTable(&buf, []criterionAUC{
		{ID: "verdict", AUC: 0.64, N: 50, Valid: true},
		{ID: "test_coverage", N: 3, Valid: false},
	})
	out := buf.String()
	if !strings.Contains(out, "verdict") || !strings.Contains(out, "0.64") {
		t.Errorf("missing scored row: %s", out)
	}
	if !strings.Contains(out, "insufficient data") {
		t.Errorf("missing invalid-row marker: %s", out)
	}
}
