package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// PR is the part of the pull request we show to the model.
type PR struct {
	URL          string `json:"url"`
	Number       int    `json:"number"`
	Title        string `json:"title"`
	Body         string `json:"body"`
	BaseRefName  string `json:"base_branch"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	ChangedFiles int    `json:"changed_files"`

	headSHA string
	owner   string
	repo    string
}

// State is what we send to TypeSafe as the thing to evaluate.
type State struct {
	PR         PR     `json:"pull_request"`
	Guidelines string `json:"project_guidelines,omitempty"`
	Diff       string `json:"diff"`
}

var prURLRe = regexp.MustCompile(`^(?:https?://)?github\.com/([^/]+)/([^/]+)/pull/(\d+)`)

func parsePRURL(raw string) (owner, repo string, err error) {
	m := prURLRe.FindStringSubmatch(strings.TrimSpace(raw))
	if m == nil {
		return "", "", fmt.Errorf("not a pull request link: %q (want https://github.com/owner/repo/pull/123)", raw)
	}
	return m[1], m[2], nil
}

func gh(args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	// gh colourises --json output when colour is forced, which breaks the parse.
	cmd.Env = append(os.Environ(), "CLICOLOR_FORCE=0", "NO_COLOR=1")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("gh %s: %s", strings.Join(args, " "), msg)
	}
	return out.String(), nil
}

func fetchPR(url string) (PR, error) {
	owner, repo, err := parsePRURL(url)
	if err != nil {
		return PR{}, err
	}

	fields := "number,title,body,baseRefName,headRefOid,additions,deletions,changedFiles"
	raw, err := gh("pr", "view", url, "--json", fields)
	if err != nil {
		return PR{}, err
	}

	var v struct {
		Number       int    `json:"number"`
		Title        string `json:"title"`
		Body         string `json:"body"`
		BaseRefName  string `json:"baseRefName"`
		HeadRefOid   string `json:"headRefOid"`
		Additions    int    `json:"additions"`
		Deletions    int    `json:"deletions"`
		ChangedFiles int    `json:"changedFiles"`
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return PR{}, fmt.Errorf("decode pr view: %w", err)
	}

	body := strings.TrimSpace(v.Body)
	if body == "" {
		body = "(no description)"
	}

	return PR{
		URL: url, Number: v.Number, Title: v.Title, Body: body,
		BaseRefName: v.BaseRefName, Additions: v.Additions,
		Deletions: v.Deletions, ChangedFiles: v.ChangedFiles,
		headSHA: v.HeadRefOid, owner: owner, repo: repo,
	}, nil
}

// fetchDiff fails loudly on an empty diff: reviewing nothing produces a
// confident nonsense verdict. Happens on Gerrit-mirrored repos like golang/go.
func fetchDiff(url string, maxBytes int) (string, error) {
	diff, err := gh("pr", "diff", url)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(diff) == "" {
		return "", fmt.Errorf("empty diff for %s: nothing to review (mirrored pull request, or the branch is gone)", url)
	}
	return truncate(diff, maxBytes), nil
}

// fetchGuidelines returns the first project doc that exists on the PR head.
func fetchGuidelines(pr PR, names []string, maxBytes int) (string, string) {
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		path := fmt.Sprintf("repos/%s/%s/contents/%s?ref=%s", pr.owner, pr.repo, name, pr.headSHA)
		out, err := gh("api", "-H", "Accept: application/vnd.github.raw", path)
		if err != nil || strings.TrimSpace(out) == "" {
			continue
		}
		return name, truncate(out, maxBytes)
	}
	return "", ""
}

func truncate(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + "\n\n[... truncated, original was " + fmt.Sprint(len(s)) + " bytes ...]"
}
