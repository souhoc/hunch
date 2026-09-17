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
task                             # lint, test, build
task cover                       # coverage gate, fails under MIN_COVERAGE
task cover MIN_COVERAGE=80       # try a stricter gate without editing anything
go test -run TestGoodness -v     # one test
```

CI (`.github/workflows/ci.yml`) runs exactly these plus `-race`, and installs
zsh and fish so the completion-script syntax checks do not skip themselves.
`MIN_COVERAGE` is a ratchet, not a target: raise it in both `Taskfile.yml` and
the workflow when coverage climbs.

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
- **Completions are generated from the `*flag.FlagSet`, never a hand-kept list.**
  `registerFlags` exists so the flags are introspectable outside `main` — `go test`
  registers its own flags on `flag.CommandLine`, so tests build their own set.
  The `completions` subcommand must stay absent from its own output.
- **`Blocks` overrides the verdict, and the renderer never names the ids.** A
  criterion marked `Blocks: true` whose answer is red prints a banner above
  everything, because `verdict` is the weakest scored criterion and approve-biased
  — a confident `weakens_security: yes` has to beat it, not sit below it in the
  list. Blocking and red share one threshold (`redAt`), so the report can never
  paint a criterion green and block on it at the same time. `TestBlockAtRedBoundary`
  guards that. Adding a blocker is a data change in `criteria.go`, never a change
  to `render.go`.
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
follows_project_conventions                              0.46   dropped, no signal
test_coverage                                            0.20   dropped, anti-correlated
```

`follows_project_conventions` and `test_coverage` were removed from the rubric on
the strength of those two numbers. `test_coverage` is the one to think twice about
before re-adding: it reads the diff correctly and 0.20 is as far from chance as
0.80, so it carries signal — but inverted, on n=5 per group, almost certainly
through a confound (bigger, riskier changes attract both tests and scrutiny).
Inverting it would be fitting the noise. If either comes back, it needs a fresh
measurement, not this table.

Separately, `code_review_effort` was spot-checked on 6 PRs of deliberately
different shape and separated them cleanly: dependency bump and docs-only both
`skip` at 100%, a one-file fix `low` 71%, a goroutine-deadlock fix `high` 95%, a
CommonJS-semantics change `high` 90%, a 24-file refactor `high` 72% / `max` 25%.
It never picked `medium` — the model jumps from `low` to `high`, so treat that
bucket as unused rather than meaningful.

`diff_dilution`, `weakens_security` and `comment_noise` were spot-checked on 7 PRs of
deliberately different shape. No AUC — none of these PRs have review-decision ground
truth, so this says the criteria read the diff correctly, not that they predict anything.

- `weakens_security` separated a PR disabling TLS verification (`yes` 83%) from one
  tightening the same `InsecureSkipVerify` guard (`no` 3%) — it reads the semantics, not
  the keyword. On that first PR `verdict` said `comment` at 63% and never escalated,
  which is the approve-bias above, caught live.
- `diff_dilution` spanned its range: a 1-file change `Concentrated` 90%, a 117-file
  dependency-bump-plus-regeneration `Buried` 69%, and a one-line dependency removal under
  246 lines of lockfile `Buried` 67%. On a revert it answered `Buried` at 4% confidence —
  honest uncertainty rather than a confident wrong answer.
- `comment_noise` returned `Slight` 92% on a PR whose added comments were one genuine
  "why" and four restatements, and `Clean` 88-99% wherever no comments were added. Its
  top two levels, `Noisy` and `Heavy`, have never fired and are untested.

An `unexplained_removals` criterion was tried here and removed. It tracked deletion volume
rather than whether the description accounted for the removals, and answered `yes` 75% on a
PR whose body named every removed category by hand. A criterion that punishes the authors
who write the best descriptions is worse than no criterion.

Two things follow when tuning the rubric:

- The `score` criteria rank better than `verdict`, which is also approve-biased
  and prints unearned confidence. Do not treat `verdict` as the headline answer
  just because it is listed first. The `Blocks` banner exists because of this: on
  a PR disabling TLS verification, `verdict` said `comment` at 63% and never
  escalated, while `weakens_security` sat at 83% two-thirds of the way down the
  report.
- `test_coverage` reads the diff correctly; it simply does not track review
  outcome. Criteria can be accurate and non-predictive at once.

Merge-vs-close is **not** usable ground truth: closures are dominated by CLA
bots, duplicates and supersession, none of which are visible in the diff.
