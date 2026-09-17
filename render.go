package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	green = lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#3fb950"}
	red   = lipgloss.AdaptiveColor{Light: "#cf222e", Dark: "#f85149"}
	amber = lipgloss.AdaptiveColor{Light: "#9a6700", Dark: "#d29922"}
	blue  = lipgloss.AdaptiveColor{Light: "#0969da", Dark: "#58a6ff"}
	grey  = lipgloss.AdaptiveColor{Light: "#57606a", Dark: "#8b949e"}

	headerBox   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(grey).Padding(0, 1)
	titleStyle  = lipgloss.NewStyle().Bold(true)
	mutedStyle  = lipgloss.NewStyle().Foreground(grey)
	idStyle     = lipgloss.NewStyle().Bold(true).Foreground(blue)
	promptStyle = lipgloss.NewStyle().Foreground(grey).PaddingLeft(2)
	rowStyle    = lipgloss.NewStyle().PaddingLeft(2)
	trackStyle  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#d0d7de", Dark: "#30363d"})
)

const barWidth = 20

// redAt is the goodness at or below which an answer renders red — and, for a
// Blocks criterion, overrides an approving verdict. One threshold for both, so
// the report can never paint a criterion green and block on it at the same time.
const redAt = 0.33

func fg(c lipgloss.TerminalColor) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

// report prints one block per criterion, verdict first.
func report(w io.Writer, pr PR, docName string, questions map[string]Question, resp *response, pricePerMTok float64) {
	meta := fmt.Sprintf("%d %s  +%d  -%d  onto %s",
		pr.ChangedFiles, plural(pr.ChangedFiles, "file"), pr.Additions, pr.Deletions, pr.BaseRefName)
	if docName != "" {
		meta += "  ·  guidelines from " + docName
	}
	header := strings.Join([]string{
		titleStyle.Render(fmt.Sprintf("#%d  %s", pr.Number, pr.Title)),
		mutedStyle.Render(pr.URL),
		mutedStyle.Render(meta),
	}, "\n")
	fmt.Fprintln(w, headerBox.Render(header))
	fmt.Fprintln(w)

	if ids := blockers(questions, resp.Answers); len(ids) > 0 {
		fmt.Fprintln(w, fg(red).Bold(true).Render("⛔ approval blocked by "+strings.Join(ids, ", "))+
			mutedStyle.Render("  whatever verdict says"))
		fmt.Fprintln(w)
	}

	for _, id := range orderedIDs(resp.Answers) {
		q := questions[id]
		fmt.Fprintln(w, renderAnswer(id, q, resp.Answers[id]))
		fmt.Fprintln(w)
	}

	fmt.Fprintln(w, nextStep(pr, questions, resp.Answers))
	cost := float64(resp.Usage.InputTokens) / 1e6 * pricePerMTok
	fmt.Fprintln(w, mutedStyle.Render(fmt.Sprintf("%s  ·  %d in / %d out tokens  ·  %s  (output free)",
		resp.Model, resp.Usage.InputTokens, resp.Usage.OutputTokens, money(cost))))
}

// nextStep turns the code_review_effort answer into the command to run.
// Custom rubrics without that criterion get nothing.
func nextStep(pr PR, questions map[string]Question, answers map[string]Answer) string {
	a, ok := answers["code_review_effort"]
	if !ok || a.Choice == "" {
		return ""
	}
	if a.Choice == "skip" {
		return fg(green).Render("→ no code review needed") + mutedStyle.Render("  (nothing to find in this diff)")
	}
	return fg(colorOf(goodness(questions["code_review_effort"], a))).Bold(true).
		Render(fmt.Sprintf("→ /code-review %s %d", a.Choice, pr.Number)) +
		mutedStyle.Render("  in "+pr.owner+"/"+pr.repo)
}

// orderedIDs sorts ids alphabetically but keeps "verdict" on top.
func orderedIDs(answers map[string]Answer) []string {
	ids := make([]string, 0, len(answers))
	for id := range answers {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if (ids[i] == "verdict") != (ids[j] == "verdict") {
			return ids[i] == "verdict"
		}
		return ids[i] < ids[j]
	})
	return ids
}

// blockers lists the criteria answered badly enough to override an approving
// verdict. verdict is the weakest scored criterion in the rubric and carries an
// approve bias, so a confident "yes, this removes a security control" has to beat
// it rather than sit below it in the list. A rubric that marks nothing Blocks
// gets no banner.
func blockers(questions map[string]Question, answers map[string]Answer) []string {
	var ids []string
	for id, a := range answers {
		if q := questions[id]; q.Blocks && goodness(q, a) <= redAt {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func renderAnswer(id string, q Question, a Answer) string {
	headline, body := "", ""

	switch a.Type {
	case "noul":
		if a.Noul == nil {
			return idStyle.Render(id) + mutedStyle.Render("  (no answer)")
		}
		g := goodness(q, a)
		headline = fg(colorOf(g)).Render(yesNo(*a.Noul)) +
			mutedStyle.Render(fmt.Sprintf("  (%.0f%% yes)", *a.Noul*100))
		body = rowStyle.Render(bar(*a.Noul, colorOf(g)))

	case "choice":
		headline = fg(colorOf(goodness(q, a))).Bold(true).Render(a.Choice) +
			confidence(a.Confidence)
		body = distribution(a.Probabilities, nil, a.Choice, goodness(q, a))

	case "score":
		if a.Score == nil {
			return idStyle.Render(id) + mutedStyle.Render("  (no answer)")
		}
		top := float64(len(a.Legend) - 1)
		g := goodness(q, a)
		label := short(a.Legend[likeliest(a.Probabilities)])
		headline = fg(colorOf(g)).Bold(true).Render(label) +
			mutedStyle.Render(fmt.Sprintf("  %.1f/%.0f", *a.Score, top)) + confidence(a.Confidence)
		body = distribution(a.Probabilities, a.Legend, likeliest(a.Probabilities), g)

	default:
		return idStyle.Render(id) + mutedStyle.Render(fmt.Sprintf("  (unknown answer type %q)", a.Type))
	}

	lines := []string{idStyle.Render(id) + mutedStyle.Render("  →  ") + headline}
	if q.Instructions != "" {
		lines = append(lines, promptStyle.Render(q.Instructions))
	}
	return strings.Join(append(lines, body), "\n")
}

// distribution prints every option, most likely first; the winner keeps the sentiment colour.
func distribution(probs map[string]float64, legend map[string]string, winner string, g float64) string {
	keys := make([]string, 0, len(probs))
	for k := range probs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if probs[keys[i]] != probs[keys[j]] {
			return probs[keys[i]] > probs[keys[j]]
		}
		return keys[i] < keys[j] // ties keep a stable order
	})

	var rows []string
	for _, k := range keys {
		label := k
		var colour lipgloss.TerminalColor = grey
		if l, ok := legend[k]; ok {
			label = l
		}
		if k == winner {
			colour = colorOf(g)
		}
		rows = append(rows, rowStyle.Render(fmt.Sprintf("%s %s  %s",
			bar(probs[k], colour),
			fg(colour).Render(fmt.Sprintf("%3.0f%%", probs[k]*100)),
			mutedStyle.Render(label))))
	}
	return strings.Join(rows, "\n")
}

func bar(ratio float64, colour lipgloss.TerminalColor) string {
	filled := int(ratio*barWidth + 0.5)
	filled = min(max(filled, 0), barWidth)
	return fg(colour).Render(strings.Repeat("█", filled)) +
		trackStyle.Render(strings.Repeat("░", barWidth-filled))
}

// goodness maps an answer onto 0 (bad) .. 1 (good) using the question's Good field.
// Questions with no Good field come back neutral.
func goodness(q Question, a Answer) float64 {
	switch {
	case a.Type == "noul" && a.Noul != nil && q.Good == GoodYes:
		return *a.Noul
	case a.Type == "noul" && a.Noul != nil && q.Good == GoodNo:
		return 1 - *a.Noul
	case a.Type == "score" && a.Score != nil && len(a.Legend) > 1:
		ratio := *a.Score / float64(len(a.Legend)-1)
		if q.Good == GoodLow {
			return 1 - ratio
		}
		if q.Good == GoodHigh {
			return ratio
		}
	case a.Type == "choice":
		if v, ok := q.GoodChoices[a.Choice]; ok {
			return v
		}
	}
	return 0.5
}

func colorOf(g float64) lipgloss.TerminalColor {
	switch {
	case g >= 0.66:
		return green
	case g <= redAt:
		return red
	default:
		return amber
	}
}

func yesNo(v float64) string {
	switch {
	case v >= 0.66:
		return "yes"
	case v <= 0.33:
		return "no"
	default:
		return "unclear"
	}
}

func confidence(c *float64) string {
	if c == nil {
		return ""
	}
	return mutedStyle.Render(fmt.Sprintf("  (%.0f%% confident)", *c*100))
}

func likeliest(probs map[string]float64) string {
	best, bestP := "", -1.0
	for k, p := range probs {
		if p > bestP {
			best, bestP = k, p
		}
	}
	return best
}

// short keeps the part of a rubric level before its colon: "Moderate: touches..." -> "Moderate".
// money keeps sub-cent costs legible instead of printing $0.00.
func money(usd float64) string {
	if usd < 0.01 {
		return fmt.Sprintf("$%.5f", usd)
	}
	return fmt.Sprintf("$%.2f", usd)
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

func short(s string) string {
	if head, _, found := strings.Cut(s, ":"); found {
		return head
	}
	return s
}
