package main

// Which answer is the good one. Display only — used to colour the report.
const (
	GoodYes  = "yes"  // noul: 1 is good
	GoodNo   = "no"   // noul: 0 is good
	GoodLow  = "low"  // score: level 0 is good
	GoodHigh = "high" // score: the top level is good
)

// Question is one typed question sent to TypeSafe.
// Criteria is: object (noul), map[string]string (choice), []string (score).
type Question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`

	// Good, GoodChoices and Blocks never reach the API; apiQuestions strips them.
	Good        string             `json:"good,omitempty"`
	GoodChoices map[string]float64 `json:"good_choices,omitempty"` // choice option -> 0 bad .. 1 good
	Blocks      bool               `json:"blocks,omitempty"`       // a red answer here overrides an approving verdict
}

// apiQuestions drops the display-only fields so the request body stays valid.
func apiQuestions(questions map[string]Question) map[string]Question {
	out := make(map[string]Question, len(questions))
	for id, q := range questions {
		q.Good, q.GoodChoices, q.Blocks = "", nil, false
		out[id] = q
	}
	return out
}

// defaultQuestions is the proposed review rubric. Override with -questions file.json.
func defaultQuestions() map[string]Question {
	return map[string]Question{
		"verdict": {
			Type:         "choice",
			Instructions: "As a reviewer, what is the right outcome for this pull request?",
			Criteria: map[string]string{
				"approve":         "Correct, in scope, ready to merge as is",
				"comment":         "Mergeable but has nits or questions worth raising",
				"request_changes": "Has a defect, a missing test, or scope that must change before merge",
			},
			GoodChoices: map[string]float64{"approve": 1, "comment": 0.5, "request_changes": 0},
		},
		"code_review_effort": {
			Type: "choice",
			Instructions: "How much automated code-review effort does this diff deserve? " +
				"Deeper review costs more and surfaces more uncertain findings; on a trivial diff it is wasted.",
			Criteria: map[string]string{
				"skip":   "Nothing to find: version bump, lockfile, generated file, docs, pure rename or reformatting",
				"low":    "Small self-contained logic change; only obvious, high-confidence bugs are worth reporting",
				"medium": "Ordinary fix or feature touching real logic across a few files",
				"high":   "Subtle logic, concurrency, error paths, or a change to a public contract; uncertain findings are worth seeing",
				"max":    "Wide blast radius: security, auth, money, data migration, or many files of interlocking change",
			},
			GoodChoices: map[string]float64{"skip": 1, "low": 0.8, "medium": 0.55, "high": 0.3, "max": 0},
		},
		"description_matches_diff": {
			Type:         "noul",
			Instructions: "Does the pull request description accurately and completely describe what the diff actually does?",
			Criteria: map[string]string{
				"true":  "Description covers every meaningful change in the diff",
				"false": "Description is empty, vague, stale, or hides changes present in the diff",
			},
			Good: GoodYes,
		},
		"in_scope": {
			Type:         "noul",
			Instructions: "Is every hunk in the diff justified by the stated purpose of the pull request?",
			Criteria: map[string]string{
				"true":  "One coherent change; no drive-by refactors, reformatting, or unrelated files",
				"false": "Mixes unrelated work, opportunistic renames, or noisy formatting churn",
			},
			Good: GoodYes,
		},
		"leaks_secrets": {
			Type:         "noul",
			Instructions: "Does the diff add a secret, credential, token, private key, or real customer data?",
			Criteria: map[string]string{
				"true":  "A real-looking secret or personal data is committed",
				"false": "No secrets; placeholders and env lookups only",
			},
			Good:   GoodNo,
			Blocks: true,
		},
		"weakens_security": {
			Type: "noul",
			Instructions: "Does the diff remove, bypass, or loosen a security control that already existed " +
				"(auth check, permission test, input validation, TLS verification, sandbox, rate limit)?",
			Criteria: map[string]string{
				"true":  "A control that existed is removed, made optional, or narrowed",
				"false": "No control weakened; checks added, unchanged, or moved intact",
			},
			Good:   GoodNo,
			Blocks: true,
		},
		"correctness_risk": {
			Type:         "score",
			Instructions: "How likely is this diff to introduce a bug, regression, or production incident?",
			Criteria: []string{
				"Safe: trivial or fully covered change",
				"Low: straightforward logic, edge cases handled",
				"Moderate: touches real logic, some edge cases unclear",
				"High: unhandled edge case, race, or breaking change to a public contract",
			},
			Good: GoodLow,
		},
		"unneeded_complexity": {
			Type:         "score",
			Instructions: "How much complexity does this diff add beyond what the problem requires (layers, abstractions, options, indirection)?",
			Criteria: []string{
				"Minimal: simplest thing that works",
				"Slight: a little more machinery than needed",
				"Speculative: premature abstraction or generalisation",
				"Heavy: framework-scale complexity for a small problem",
			},
			Good: GoodLow,
		},
		"review_effort": {
			Type:         "score",
			Instructions: "How much effort does a human reviewer need to review this pull request properly?",
			Criteria: []string{
				"Minutes: small and obvious",
				"Focused: medium size, one concern",
				"Careful: large or subtle",
				"Split: should be several pull requests",
			},
			Good: GoodLow,
		},
		"diff_dilution": {
			Type: "score",
			Instructions: "What fraction of this diff is load-bearing — the code that actually delivers the stated " +
				"purpose, rather than the plumbing, wiring, and mechanical churn that exists only to let it run?",
			Criteria: []string{
				"Concentrated: nearly every hunk answers the stated purpose",
				"Plumbing: a small core plus the wiring it genuinely needs",
				"Buried: the core is a fraction of the diff, the rest is mechanical or incidental",
				"Several changes: more than one coherent concern in one pull request",
			},
			Good: GoodLow,
		},
		"comment_noise": {
			Type: "score",
			Instructions: "How much of the commentary added in this diff fails to earn its place? A good comment " +
				"explains why the code is the way it is, or names a constraint the code cannot state. A bad one restates " +
				"the code, narrates the change or its history, or addresses the reviewer.",
			Criteria: []string{
				"Clean: comments added explain why, or none were needed",
				"Slight: some restating of what the code already says",
				"Noisy: comments narrate the change or its history instead of the code",
				"Heavy: verbose blocks, commented-out code, or commentary aimed at the reviewer",
			},
			Good: GoodLow,
		},
	}
}
