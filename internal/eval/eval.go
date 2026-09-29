// Package eval scores the rubric against a corpus of pull requests with known
// human review decisions.
package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/souhoc/hunch/v2/internal/github"
	"github.com/souhoc/hunch/v2/internal/review"
	"github.com/souhoc/hunch/v2/internal/typesafe"
)

// CorpusEntry is one row of eval/corpus.json: a real pull request with its
// human review decision, used as ground truth for `hunch eval`.
//
// FinalState is context only — merge/close status is not usable ground
// truth (closures are dominated by CLA bots, duplicates and supersession),
// so the scorer never reads it.
type CorpusEntry struct {
	URL            string `json:"url"`
	ReviewDecision string `json:"review_decision"` // "approved" | "changes_requested"
	FinalState     string `json:"final_state,omitempty"`
	Reason         string `json:"reason"`
}

// LoadCorpus reads and validates a corpus file.
func LoadCorpus(path string) ([]CorpusEntry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entries []CorpusEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%s: no entries", path)
	}
	for i, e := range entries {
		if e.URL == "" {
			return nil, fmt.Errorf("%s: entry %d: missing url", path, i)
		}
		if e.ReviewDecision != "approved" && e.ReviewDecision != "changes_requested" {
			return nil, fmt.Errorf("%s: entry %d (%s): review_decision must be %q or %q, got %q",
				path, i, e.URL, "approved", "changes_requested", e.ReviewDecision)
		}
	}
	return entries, nil
}

// Result is one corpus entry's outcome: the answers hunch gave, or why
// it was skipped.
type Result struct {
	URL     string                     `json:"url"`
	Label   bool                       `json:"approved"`
	Answers map[string]typesafe.Answer `json:"answers,omitempty"`
	Err     string                     `json:"error,omitempty"`
}

// Run gathers and evaluates every corpus entry the same way a normal
// single-PR run does. A failed entry is skipped, not fatal — 50 real network
// calls will have some flakiness, and a partial result beats none.
func Run(c *typesafe.Client, questions map[string]review.Question, corpus []CorpusEntry, o github.Options, logf func(format string, a ...any)) []Result {
	apiQ := review.APIQuestions(questions)
	results := make([]Result, 0, len(corpus))
	for i, entry := range corpus {
		logf("evaluating %d/%d: %s", i+1, len(corpus), entry.URL)
		r, err := evalOne(c, apiQ, o, entry)
		if err != nil {
			log.Printf("skip %s: %v", entry.URL, err)
			r.Err = err.Error()
		}
		results = append(results, r)
	}
	return results
}

func evalOne(c *typesafe.Client, apiQ map[string]typesafe.Question, o github.Options, entry CorpusEntry) (Result, error) {
	r := Result{URL: entry.URL, Label: entry.ReviewDecision == "approved"}

	state, err := github.Gather(entry.URL, o, func(string, ...any) {})
	if err != nil {
		return r, err
	}
	resp, err := c.Evaluate(state, apiQ)
	if err != nil {
		return r, err
	}
	r.Answers = resp.Answers
	return r, nil
}

// AnySucceeded reports whether at least one entry has answers to score.
func AnySucceeded(results []Result) bool {
	for _, r := range results {
		if r.Err == "" {
			return true
		}
	}
	return false
}

// CriterionAUC is one row of the accuracy report.
type CriterionAUC struct {
	ID    string
	AUC   float64
	N     int
	Valid bool // false: one label class was never seen, or nothing answered
}

// AUCTable scores every criterion that appears in the results against the
// corpus's ground-truth label, using the same review.Goodness polarity math the
// normal report already renders with — so a criterion's AUC here means
// exactly what its colour means in a single-PR report.
func AUCTable(results []Result, questions map[string]review.Question) []CriterionAUC {
	scores := map[string][]float64{}
	labels := map[string][]bool{}
	for _, r := range results {
		for id, a := range r.Answers {
			if !answered(a) {
				continue
			}
			scores[id] = append(scores[id], review.Goodness(questions[id], a))
			labels[id] = append(labels[id], r.Label)
		}
	}

	ids := make([]string, 0, len(scores))
	for id := range scores {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	table := make([]CriterionAUC, 0, len(ids))
	for _, id := range ids {
		a, err := auc(scores[id], labels[id])
		table = append(table, CriterionAUC{ID: id, AUC: a, N: len(scores[id]), Valid: err == nil})
	}
	sort.SliceStable(table, func(i, j int) bool {
		if table[i].Valid != table[j].Valid {
			return table[i].Valid // valid rows first
		}
		return table[i].AUC > table[j].AUC // most predictive first
	})
	return table
}

// answered reports whether a has an actual value for its type, so an
// unanswered question doesn't silently pad the AUC with a neutral score.
func answered(a typesafe.Answer) bool {
	switch a.Type {
	case "noul":
		return a.Noul != nil
	case "score":
		return a.Score != nil
	case "choice":
		return a.Choice != ""
	default:
		return false
	}
}

// auc is the area under the ROC curve via the Mann-Whitney U / rank-sum
// statistic — the same one sklearn.roc_auc_score computes, so the numbers
// stay comparable to previous ad-hoc measurements. Every score is ranked
// ascending; a tied block of scores shares the average rank across that
// block, so ties contribute their fair share rather than an arbitrary order.
func auc(scores []float64, labels []bool) (float64, error) {
	if len(scores) != len(labels) {
		return 0, fmt.Errorf("scores and labels length mismatch: %d vs %d", len(scores), len(labels))
	}

	type pair struct {
		score float64
		label bool
	}
	pairs := make([]pair, len(scores))
	for i := range scores {
		pairs[i] = pair{scores[i], labels[i]}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].score < pairs[j].score })

	ranks := make([]float64, len(pairs))
	for i := 0; i < len(pairs); {
		j := i
		for j < len(pairs) && pairs[j].score == pairs[i].score {
			j++
		}
		avgRank := float64(i+1+j) / 2 // ranks i+1..j (1-based), averaged over the tied block
		for k := i; k < j; k++ {
			ranks[k] = avgRank
		}
		i = j
	}

	var rankSumPos float64
	var nPos, nNeg int
	for i, p := range pairs {
		if p.label {
			rankSumPos += ranks[i]
			nPos++
		} else {
			nNeg++
		}
	}
	if nPos == 0 || nNeg == 0 {
		return 0, fmt.Errorf("need both classes represented, got %d approved, %d changes_requested", nPos, nNeg)
	}
	u := rankSumPos - float64(nPos*(nPos+1))/2
	return u / float64(nPos*nNeg), nil
}

// PrintAUCTable prints one row per scored criterion, most predictive first.
func PrintAUCTable(w io.Writer, table []CriterionAUC) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "criterion\tauc\tn")
	for _, row := range table {
		if !row.Valid {
			fmt.Fprintf(tw, "%s\tinsufficient data\t%d\n", row.ID, row.N)
			continue
		}
		fmt.Fprintf(tw, "%s\t%.2f\t%d\n", row.ID, row.AUC, row.N)
	}
	tw.Flush()
}
