# hunch

Review a GitHub pull request with the [TypeSafe](https://docs.typesafe.ai) evaluation
endpoint. It gathers the PR, asks a rubric of typed questions, and prints the answers
with their probability bars (rendered with [lipgloss](https://github.com/charmbracelet/lipgloss),
plain text when piped).

## What it sends

The `state` is one JSON object:

- `pull_request` — number, title, **body**, base branch, files/additions/deletions
- `project_guidelines` — `CLAUDE.md` if the repo has one, else `README.md`, read at the PR head
- `diff` — output of `gh pr diff`

Diff and doc are truncated (`-max-diff`, `-max-doc`) so a huge PR does not blow up the request.

## Install

```sh
go install github.com/souhoc/hunch@latest
```

The API key comes from `TYPESAFE_API_KEY`, or from [skate](https://github.com/charmbracelet/skate):

```sh
skate set typesafe:hunch apikey_...
```

Needs `gh` installed and logged in.

## Use

```sh
hunch https://github.com/owner/repo/pull/123
hunch -v -json https://github.com/owner/repo/pull/123
hunch -dump-state https://github.com/owner/repo/pull/123   # see the payload, no API call
hunch -dump-questions > my-rubric.json                     # start from the defaults
hunch -questions my-rubric.json https://github.com/owner/repo/pull/123
```

## Shell completions

```sh
hunch completions fish > ~/.config/fish/completions/hunch.fish
hunch completions bash > /usr/local/etc/bash_completion.d/hunch
hunch completions zsh  > "${fpath[1]}/_hunch"
```

The scripts are generated from the flags themselves, so they cannot drift. The
`completions` subcommand is deliberately left out of them.

## Development

Uses [Task](https://taskfile.dev):

```sh
task            # lint, test, build
task cover      # coverage, fails below MIN_COVERAGE
task --list     # everything else
```

CI runs the same checks on every push and pull request.

## Criteria

| id | type | asks |
| --- | --- | --- |
| `verdict` | choice | approve / comment / request_changes |
| `code_review_effort` | choice | skip / low / medium / high / max — how deep a `/code-review` this deserves |
| `description_matches_diff` | noul | does the body describe what the diff does |
| `in_scope` | noul | one coherent change, no drive-by churn |
| `leaks_secrets` | noul | credentials or customer data committed — **blocks** |
| `weakens_security` | noul | an existing control removed or loosened — **blocks** |
| `correctness_risk` | score | 0 safe → 3 high |
| `unneeded_complexity` | score | 0 simplest → 3 framework-scale |
| `review_effort` | score | 0 minutes → 3 should be split |
| `diff_dilution` | score | 0 concentrated → 3 several changes in one PR |
| `comment_noise` | score | 0 comments earn their place → 3 verbose or narrating |

They live in `criteria.go`. Edit there for a permanent change, or use `-questions` for a one-off.

`follows_project_conventions` and `test_coverage` used to be here and were dropped:
measured against human review decisions they scored 0.46 and 0.20 AUC, where 0.5 is
a coin flip. See `CLAUDE.md` before re-adding either.

Three extra fields are ours, and are stripped before the request is sent:

- `good` — `"yes"` / `"no"` for a noul, `"low"` / `"high"` for a score
- `good_choices` — for a choice: option → `0` (bad) .. `1` (good)
- `blocks` — a red answer here prints a banner above the report and overrides an
  approving verdict

Leave `good` out and the criterion renders amber.

## Blocking

`verdict` is the weakest scored criterion in the rubric and carries an approve
bias, so two criteria outrank it. When either answers red, the report opens with:

```
⛔ approval blocked by weakens_security  whatever verdict says
```

On a real pull request disabling TLS certificate verification, `verdict` said
`comment` at 61% while `weakens_security` said `yes` at 83%. The banner is there so
the strongest signal is not the one you have to scroll to.

## The next-step line

`code_review_effort` picks how much automated review the diff is worth, and the
report ends with the command to run:

```
→ /code-review high 1806  in charmbracelet/bubbletea
→ no code review needed  (nothing to find in this diff)
```

The built-in skill also accepts `xhigh` between `high` and `max`; the rubric does
not offer it, bump by hand if you want it. Drop the criterion from your rubric and
the line disappears.

## Flags

```
-model string      TypeSafe model (default "jev-latest")
-questions file    JSON file overriding the default criteria
-docs list         docs to look for, first match wins (default "CLAUDE.md,README.md")
-max-diff n        truncate the diff to n bytes (default 150000)
-max-doc n         truncate the project doc to n bytes (default 30000)
-retries n         retries on 429/529 with exponential backoff (default 3)
-dump-questions    print the default criteria as JSON and exit
-dump-state        print the state that would be sent and exit
-json              print the raw API response
-v                 log progress to stderr
```

## Files

- `main.go` — flags and orchestration
- `gather.go` — `gh` calls, state building
- `typesafe.go` — API client, retry, answer types
- `criteria.go` — the rubric
- `render.go` — lipgloss report
