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

Every percentage quoted in this file is one run, not a constant. The same PR
re-run moves a couple of points — the TLS-verification anecdote below came back
`comment` at 63% and then 61% on consecutive runs. Quote a figure as evidence of
which way a criterion leans, never as a fixed value to assert in a test.

`diff_dilution`, `weakens_security` and `comment_noise` were spot-checked on 7 PRs of
deliberately different shape. No AUC — none of these PRs have review-decision ground
truth, so this says the criteria read the diff correctly, not that they predict anything.

- `weakens_security` separated a PR disabling TLS verification (`yes` 83%) from one
  tightening the same `InsecureSkipVerify` guard (`no` 3%) — it reads the semantics, not
  the keyword. On that first PR `verdict` said `comment` at 61% and never escalated,
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
  a PR disabling TLS verification, `verdict` said `comment` at 61% and never
  escalated, while `weakens_security` sat at 83% two-thirds of the way down the
  report.
- `test_coverage` reads the diff correctly and still scored 0.20. A criterion can
  be accurate about the diff and worthless — or worse, backwards — as a predictor
  of what a reviewer will do. Accuracy and predictiveness are separate properties;
  measure the second, never infer it from the first.

Merge-vs-close is **not** usable ground truth: closures are dominated by CLA
bots, duplicates and supersession, none of which are visible in the diff.

## Scaling the measurement to n=25 (`eval/corpus.json`)

The n=5 numbers above were a one-off, uncommitted process — never reproducible,
never re-run when the rubric changed. `eval/corpus.json` fixes that: a checked-in
corpus of 50 real PRs (25 `approved`, 25 `changes_requested`, diverse repos and
languages, each with a synthesized one-line reason from the actual review
thread), scored by `hunch eval eval/corpus.json` any time the rubric or model
changes. AUC against the same APPROVED/CHANGES_REQUESTED ground truth, n=25 per
group:

```
diff_dilution              0.71
verdict                    0.66
review_effort              0.65
in_scope                   0.63
unneeded_complexity        0.62
correctness_risk           0.61
leaks_secrets              0.54
weakens_security           0.53
code_review_effort         0.53
comment_noise              0.50
description_matches_diff   0.46
```

What changed going from n=5 to n=25:

- `correctness_risk`, `review_effort`, and `unneeded_complexity` all pulled in
  from 0.76 toward 0.61-0.65. Still real signal, well above chance, but 0.76
  was an n=5 small-sample effect, not the criteria's true ceiling.
- `verdict` held steady (0.64 → 0.66) — still the approve-biased weakest
  scored criterion, still not the headline answer.
- `description_matches_diff` fell from 0.62 to 0.46 — indistinguishable from a
  coin flip at n=25, in the same range as the already-dropped
  `follows_project_conventions` (0.46). The n=5 number was noise. **Kept
  anyway, deliberately** — see the Conclusion section below for why this is
  not the same call as dropping `follows_project_conventions`.
- `diff_dilution` gets a real AUC for the first time (0.71, the best score in
  this run) — previously only spot-checked with no ground truth to measure
  against.
- `leaks_secrets` and `weakens_security` land near chance (0.53-0.54) against
  approve/changes_requested. That does not mean they are broken: they exist to
  catch rare, severe issues via `Blocks`, not to rank ordinary review outcomes,
  and a 50-PR sample of everyday open-source PRs almost certainly contains few
  or no real secret leaks or security regressions to separate on. Their
  earlier spot-check (a TLS-verification PR: `yes` 83% vs. a control PR
  tightening the same guard: `no` 3%) remains the relevant evidence for what
  they are actually for.
- `code_review_effort` also scores near chance (0.53) here, which does not
  contradict its spot-check on 6 PRs of different shape: that measured whether
  it sizes review depth correctly, not whether it predicts approval. A 24-file
  refactor can deserve `high` effort and still get approved — different
  questions.
- `comment_noise` sits exactly at 0.50, consistent with the earlier finding
  that its `Noisy`/`Heavy` levels rarely fire: most PRs' comments don't vary
  enough for this criterion to discriminate by outcome.

Both corpora are genuine open-source review decisions, not this repo's own —
`eval/corpus.json` is data, not a template to imitate.

**`eval/corpus.json` entries can go stale.** `hunch eval` fetches each PR's diff
fresh at run time — it does not pin a commit SHA. 17 of the 50 entries are
`final_state: "open"`, so a future re-run can see a different diff than the one
the `reason` field describes if the PR gained commits since. This already
happened once, harmlessly, within this same session: for `django/django#21169`
and `angular/angular#70710`, a same-session second-judge check (below) found the
live diff no longer matched the recorded `reason`. Merged and closed entries
don't have this problem — their diff is frozen. Re-verify the `open` entries
before trusting a `hunch eval` run that happens long after the corpus was built.

**Run-to-run variance is small and now measured, not just anecdotal.** Five
independent full runs of `hunch eval eval/corpus.json` gave per-criterion AUC
within ±0.01–0.04 of each other (worst: `leaks_secrets` at ±0.08, on very few
positive examples). Per-PR, the two `score`/`noul` answer types moved by 0.01–0.04
of goodness on average between runs; the two `choice` criteria (`verdict`,
`code_review_effort`) each flipped their discrete pick on 4 of 50 PRs (8%) across
5 runs — that flip rate is where nearly all the aggregate variance comes from.
The 50-PR aggregate numbers above are reproducible; a single PR's report can
still land on either side of a close call.

**A second LLM judge (Claude, blind to hunch's answers) independently answered
all 11 criteria on a 12-PR subset** — agreement was 89% (117/132 cells),
confirming most of the rubric reads consistently across judges, not just across
runs of the same model. Two results stand out:

- `description_matches_diff` agreement between judges was 11/12 — hunch and a
  second judge read description-vs-diff matching the same way almost every
  time. That rules out "hunch just reads it wrong" as the explanation for its
  0.46 AUC: two independent readers agree with each other and still don't
  predict the review outcome, the same accurate-but-not-predictive shape as
  `test_coverage`.
- On `traefik/traefik#13601`, hunch answered `weakens_security: no` at 4%
  confidence — wrong. The PR's own description says the new router "carr[ies]
  only the existing `<routerKey>-app-root` middleware," i.e. explicitly not the
  auth/rate-limit/IP-allowlist middleware its sibling router has; the human
  reviewer flagged exactly that, and the second judge caught it independently
  (55%, correctly) from the PR body alone. `weakens_security` is a `Blocks`
  criterion — a false "no" here means the report's banner would not have fired
  on a real security regression. One miss on 12 PRs is not a rate, but it is a
  concrete example of the failure mode `Blocks` exists to catch and didn't.

## Conclusion (n=25 measurement, current as of this corpus)

- **The rubric has real, modest, now well-measured signal.** Six criteria
  (`diff_dilution` 0.71, `verdict` 0.66, `review_effort` 0.65, `in_scope` 0.63,
  `unneeded_complexity` 0.62, `correctness_risk` 0.61) separate approved from
  changes-requested PRs meaningfully above chance, and the numbers hold across
  5 independent repeat runs — this is signal, not noise from a small sample.
- **`verdict` is confirmed to be the shakiest judgment in the rubric, not just
  approve-biased.** It has by far the lowest agreement between independent
  judges (5/12, vs. 89% everywhere else) — two competent judges converge on
  almost every other criterion and diverge most on `verdict` itself. This
  validates the existing design (`Blocks` overrides it, it is never the
  headline) rather than changing anything.
- **`description_matches_diff` is kept, deliberately, despite a 0.46 AUC.**
  Unlike `follows_project_conventions` (also 0.46, dropped) and `test_coverage`
  (0.20, inverted — actively misleading), `description_matches_diff` is neither
  wrong nor confounded: a second judge agreed with hunch's reading 11/12 times.
  It simply answers a question ("does this description describe this diff?")
  that is useful to know before reviewing regardless of whether it predicts the
  outcome. AUC measures predictiveness, not usefulness — a low AUC is a reason
  to stop calling a criterion a predictor, not automatically a reason to remove
  it from the report. Re-evaluate this if it ever becomes actively misleading
  the way `test_coverage` was, not just quiet.
- **The one real concern is `weakens_security` missing a genuine security
  regression** (the `traefik` PR above) that both a human reviewer and a second
  LLM judge caught from the PR's own description. See `.issues/` for a proposed
  follow-up: a second, focused TypeSafe call scoped to just the
  acceptance-gating criteria, rather than folding them into the same single
  11-question call as everything else.
