---
created_at: 2026-09-17T22:42:53Z
status: todo
tags: [accuracy]
---

# 4. Second, focused jev call for the acceptance-gating criteria

## What

Today `client.evaluate` (`typesafe.go`) sends all 11 rubric criteria to
TypeSafe (`jev-latest`) in one `POST`. Add a second, separate call scoped to
just the criteria that actually gate accept/reject — the two `Blocks: true`
criteria (`leaks_secrets`, `weakens_security`) and probably `verdict` — asked
in isolation, instead of folded into the same single call as the other 8
unrelated questions (`review_effort`, `comment_noise`, `diff_dilution`, etc.).

## Why

`hunch eval` (see `CLAUDE.md`, "Scaling the measurement to n=25") found a
concrete miss: on `traefik/traefik#13601`, hunch answered `weakens_security:
no` at 4% confidence. The PR's own description says the new router "carr[ies]
only the existing `<routerKey>-app-root` middleware" — i.e. explicitly not the
auth/rate-limit/IP-allowlist middleware its sibling router has. The human
reviewer flagged exactly that, and a second LLM judge (Claude, blind to
hunch's answer) caught it independently from the PR body alone, at 55%.

`weakens_security` is a `Blocks` criterion — the one whose entire job is
catching exactly this, and whose false "no" means the report's `⛔ approval
blocked` banner (`render.go`, `blockers()`) never fires when it should. One
miss in a 50-PR eval run (and a 12-PR blind pilot) is not a rate, but it is
evidence that the highest-stakes questions may be getting diluted by being
asked in the same breath as 8 other, lower-stakes ones.

## Current state (for context, not instructions)

- `typesafe.go`: `client.evaluate(state any, questions map[string]Question)
  (*response, error)` builds one `request{State, Model, Questions}` and POSTs
  it once. There is no per-criterion call today.
- `criteria.go`: `defaultQuestions()` marks `leaks_secrets` and
  `weakens_security` with `Blocks: true`. `verdict` is the only `choice`
  criterion that maps to accept/reject directly.
- `render.go`: `blockers()` scans `questions` for `Blocks: true` entries whose
  `goodness(q, a) <= redAt` (0.33) and returns their ids; `report()` prints the
  banner above everything else when `blockers()` is non-empty.
- `eval.go` / `eval/corpus.json`: the accuracy harness that surfaced this —
  `hunch eval eval/corpus.json` re-runs the full rubric against 50 real PRs and
  prints a per-criterion AUC table. Useful for checking whether this change
  helps, though a single anecdote (`traefik`) won't move the aggregate AUC by
  itself; treat any post-change re-run as directional, not proof.

## Open questions to resolve before implementing (do not guess in this issue)

- Scope: just the two `Blocks` criteria, or `verdict` too? `verdict` is the
  other criterion that directly determines accept/reject, but it is not marked
  `Blocks` and is already known to be the weakest, most subjective judgment in
  the rubric (`CLAUDE.md` Conclusion) — a second call might not fix subjective
  disagreement the way it could fix an attention/dilution problem.
- Does asking the same model the same question in isolation actually change
  the answer, or does `jev-latest` give the same answer either way? Worth a
  small experiment (re-ask `weakens_security` alone on the `traefik` PR and a
  handful of others) before committing to building this.
- Cost: this adds a second API call per PR, always-on, or only conditionally
  (e.g. only re-ask if the first pass's answer isn't confidently "no")?
- Reconciliation: if the two calls disagree, which wins? Always the dedicated
  second call (presumably more focused), or the more alarming (lower-goodness)
  of the two?

## Acceptance criteria (draft — refine once open questions above are answered)

- A documented decision on scope (which criteria get the second call) and on
  reconciliation when the two calls disagree.
- The chosen criteria are re-asked via a second, focused TypeSafe call, and
  `render.go`'s `blockers()`/`report()` use whichever answer wins, without
  `render.go` needing to know a second call happened — keep that a
  `main.go`/new-file concern, same separation `eval.go` already keeps from
  `render.go`.
- A custom `-questions` rubric with no `Blocks` criteria triggers no second
  call at all — this is not a general "always double-check everything"
  feature.
- `task` (lint, test, build) stays clean; new tests cover the two-call flow by
  mocking two round trips instead of one (extend the `serve` helper pattern in
  `typesafe_test.go`).
- `hunch eval eval/corpus.json` still runs end to end against the new flow.

## Explicit non-goals

- Not a general multi-pass or agentic redesign of the whole rubric — scoped to
  the criteria that gate accept/reject.
- Not a claim that this fixes the `traefik` miss specifically; n=1 is not
  evidence a fix works, only that the current single-call design has at least
  one real failure mode worth testing a fix against.
