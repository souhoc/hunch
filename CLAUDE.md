# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A single-binary Go CLI that reviews a GitHub pull request by sending it to the
TypeSafe evaluation endpoint (`POST https://api.typesafe.ai/v1/systemone`) as a
`state` plus a map of typed `questions`, then renders the typed answers.

`typesafe.md` is the vendored API reference — read it before touching request or
answer shapes. It is the source of truth for the three question/answer types
(`noul`, `choice`, `score`) and their JSON.

## Commands

```sh
go build -o hunch .          # build
go test ./...                    # all tests
go test -run TestGoodness -v     # one test
go vet ./...                     # vet
gofmt -l .                       # must print nothing
```

Iterate without spending API tokens:

```sh
./hunch -dump-state <pr-url>   # exact payload, no API call
./hunch -dump-questions        # the rubric as JSON
```

Requires `gh` installed and authenticated. The API key comes from
`TYPESAFE_API_KEY`, else `skate get typesafe:hunch`.

## Flow

`main.go` orchestrates; each stage lives in its own file.

1. `gather.go` — shells out to `gh` three times: `pr view --json` (body, title,
   counts), `pr diff`, and `api .../contents/<doc>?ref=<headSHA>` for `CLAUDE.md`
   then `README.md`. All three become one `State` struct.
2. `typesafe.go` — POSTs the state, retries 429/529 with exponential backoff,
   decodes every answer shape into one flat `Answer` struct.
3. `criteria.go` — the rubric (`defaultQuestions()`), overridable with `-questions`.
4. `render.go` — lipgloss report; plain text automatically when piped.

## Invariants that are easy to break

- **`apiQuestions()` must strip `Good` and `GoodChoices` before the POST.**
  Those two fields are ours, not TypeSafe's — they drive the report colours only.
  `TestAPIQuestionsStripsDisplayFields` guards this; sending unknown fields risks
  a 422.
- **`gh()` sets `CLICOLOR_FORCE=0` and `NO_COLOR=1` on the subprocess.** With
  colour forced in the ambient environment, `gh --json` emits ANSI codes and the
  JSON parse fails.
- **An empty diff is a hard error, not an empty review.** Gerrit-mirrored repos
  (golang/go) serve pull requests with zero files; the model then returns a
  confident verdict about nothing.
- **Colour needs polarity.** `goodness()` maps an answer to 0 (bad) .. 1 (good)
  using the question's `Good` (`yes`/`no` for noul, `low`/`high` for score) or
  `GoodChoices` (option → 0..1). Without it a criterion renders amber. A new
  criterion with no polarity is silently uninformative rather than wrong — but
  `leaks_secrets: 92% yes` painted green would be a lie, so set it.
- **`nextStep()` in `render.go` special-cases one criterion id**, `code_review_effort`,
  to print the `/code-review <level> <number>` command. A rubric without that id
  prints nothing. This is the only id the renderer knows by name — keep it that way
  rather than growing a generic hook for one instance.
- **Score headlines name the modal level** (`likeliest(probabilities)`), not the
  rounded weighted score, so the headline and the bars below it agree.

## What the criteria actually measure

Measured on 10 pull requests with human `APPROVED` vs `CHANGES_REQUESTED` review
decisions (n=5 per group — directional only, wide error bars). AUC, where 0.5 is
a coin flip:

```
correctness_risk / review_effort / unneeded_complexity   0.76
verdict (1-approve)                                      0.64
description_matches_diff                                 0.62
follows_project_conventions                              0.46   no signal
test_coverage                                            0.20   anti-correlated
```

Separately, `code_review_effort` was spot-checked on 6 PRs of deliberately
different shape and separated them cleanly: dependency bump and docs-only both
`skip` at 100%, a one-file fix `low` 71%, a goroutine-deadlock fix `high` 95%, a
CommonJS-semantics change `high` 90%, a 24-file refactor `high` 72% / `max` 25%.
It never picked `medium` — the model jumps from `low` to `high`, so treat that
bucket as unused rather than meaningful.

Two things follow when tuning the rubric:

- The `score` criteria rank better than `verdict`, which is also approve-biased
  and prints unearned confidence. Do not treat `verdict` as the headline answer
  just because it is listed first.
- `test_coverage` reads the diff correctly; it simply does not track review
  outcome. Criteria can be accurate and non-predictive at once.

Merge-vs-close is **not** usable ground truth: closures are dominated by CLA
bots, duplicates and supersession, none of which are visible in the diff.
