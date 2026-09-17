package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGH puts a stub `gh` on PATH for the duration of the test.
func fakeGH(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	body := "#!/bin/sh\n" + script + "\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

const prViewJSON = `{"number":42,"title":"a title","body":" a body ","baseRefName":"main","headRefOid":"abc123","additions":7,"deletions":2,"changedFiles":3}`

func TestFetchPR(t *testing.T) {
	fakeGH(t, `case "$1 $2" in "pr view") echo '`+prViewJSON+`';; *) exit 1;; esac`)

	pr, err := fetchPR("https://github.com/owner/repo/pull/42")
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 42 || pr.Title != "a title" || pr.ChangedFiles != 3 {
		t.Errorf("bad parse: %+v", pr)
	}
	if pr.Body != "a body" {
		t.Errorf("body should be trimmed, got %q", pr.Body)
	}
	if pr.owner != "owner" || pr.repo != "repo" || pr.headSHA != "abc123" {
		t.Errorf("owner/repo/sha = %q/%q/%q", pr.owner, pr.repo, pr.headSHA)
	}
}

func TestFetchPREmptyBody(t *testing.T) {
	fakeGH(t, `echo '{"number":1,"title":"t","body":"   ","headRefOid":"x"}'`)
	pr, err := fetchPR("https://github.com/o/r/pull/1")
	if err != nil {
		t.Fatal(err)
	}
	if pr.Body != "(no description)" {
		t.Errorf("body = %q", pr.Body)
	}
}

// An empty diff must fail loudly: reviewing nothing yields a confident nonsense
// verdict. Gerrit-mirrored repos serve pull requests with zero files.
func TestFetchDiffRejectsEmpty(t *testing.T) {
	fakeGH(t, `echo ""`)
	if _, err := fetchDiff("https://github.com/o/r/pull/1", 1000); err == nil ||
		!strings.Contains(err.Error(), "nothing to review") {
		t.Errorf("got %v, want an empty-diff error", err)
	}
}

func TestFetchDiffTruncates(t *testing.T) {
	fakeGH(t, `printf 'aaaaaaaaaaaaaaaaaaaa'`)
	diff, err := fetchDiff("https://github.com/o/r/pull/1", 5)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(diff, "aaaaa") || !strings.Contains(diff, "original was 20 bytes") {
		t.Errorf("diff = %q", diff)
	}
}

func TestFetchGuidelinesFallsBackToReadme(t *testing.T) {
	// CLAUDE.md is missing, README.md is served.
	fakeGH(t, `case "$*" in *CLAUDE.md*) exit 1;; *README.md*) echo "# readme";; esac`)

	pr := PR{owner: "o", repo: "r", headSHA: "sha"}
	name, doc := fetchGuidelines(pr, []string{"CLAUDE.md", " README.md "}, 1000)
	if name != "README.md" {
		t.Errorf("name = %q, want README.md (and whitespace trimmed)", name)
	}
	if !strings.Contains(doc, "# readme") {
		t.Errorf("doc = %q", doc)
	}
}

func TestFetchGuidelinesNoneFound(t *testing.T) {
	fakeGH(t, `exit 1`)
	name, doc := fetchGuidelines(PR{owner: "o", repo: "r"}, []string{"CLAUDE.md"}, 1000)
	if name != "" || doc != "" {
		t.Errorf("want empty, got %q / %q", name, doc)
	}
}

// gh colourises --json output when colour is forced in the environment, which
// breaks the parse. gh() must neutralise that for the subprocess.
func TestGHNeutralisesForcedColour(t *testing.T) {
	fakeGH(t, `echo "CLICOLOR_FORCE=$CLICOLOR_FORCE NO_COLOR=$NO_COLOR"`)
	t.Setenv("CLICOLOR_FORCE", "1")

	out, err := gh("anything")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "CLICOLOR_FORCE=0") || !strings.Contains(out, "NO_COLOR=1") {
		t.Errorf("subprocess env not neutralised: %q", out)
	}
}

func TestGHReportsStderr(t *testing.T) {
	fakeGH(t, `echo "could not resolve to a PullRequest" >&2; exit 1`)
	if _, err := gh("pr", "view"); err == nil ||
		!strings.Contains(err.Error(), "could not resolve") {
		t.Errorf("got %v, want the stderr quoted", err)
	}
}
