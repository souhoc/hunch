package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"github.com/souhoc/hunch/v2/internal/eval"
	"github.com/souhoc/hunch/v2/internal/github"
	"github.com/souhoc/hunch/v2/internal/review"
	"github.com/souhoc/hunch/v2/internal/typesafe"
)

// cli is the whole command line. Flags shared by review and eval sit on the
// root; completions.go reads them back from the kong model, so they cannot drift.
type cli struct {
	Model     string   `default:"jev-latest" help:"TypeSafe model."`
	Questions string   `type:"existingfile" placeholder:"FILE" help:"JSON file overriding the default criteria."`
	Docs      []string `default:"CLAUDE.md,README.md" help:"Project docs to look for, first match wins."`
	Key       string   `default:"typesafe:hunch" help:"Skate key holding the API key."`
	MaxDiff   int      `default:"10000" help:"Truncate the diff to this many bytes."`
	MaxDoc    int      `default:"30000" help:"Truncate the project doc to this many bytes."`
	Retries   int      `default:"3" help:"Retries on 429/529."`
	JSON      bool     `name:"json" help:"Print the raw API response."`
	Verbose   bool     `short:"v" help:"Log progress to stderr."`

	Review      reviewCmd      `cmd:"" default:"withargs" help:"Review a pull request (the default command)."`
	Eval        evalCmd        `cmd:"" help:"Score the rubric against a corpus of reviewed pull requests."`
	Completions completionsCmd `cmd:"" help:"Print a shell completion script."`
	Version     versionCmd     `cmd:"" help:"Print the version."`
}

func newParser(c *cli) (*kong.Kong, error) {
	return kong.New(c,
		kong.Name("hunch"),
		kong.Description("Review a GitHub pull request with the TypeSafe evaluation endpoint."),
		kong.Vars{"price": fmt.Sprint(typesafe.DefaultPricePerMTok)},
	)
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("hunch: ")

	var c cli
	parser, err := newParser(&c)
	if err != nil {
		log.Fatal(err)
	}
	ctx, err := parser.Parse(os.Args[1:])
	parser.FatalIfErrorf(err)
	ctx.FatalIfErrorf(ctx.Run(&c))
}

type reviewCmd struct {
	URL           string  `arg:"" optional:"" help:"Pull request URL."`
	Price         float64 `default:"${price}" help:"USD per million input tokens, for the cost line."`
	DumpQuestions bool    `help:"Print the criteria as JSON and exit."`
	DumpState     bool    `help:"Print the state that would be sent and exit (no API call)."`
}

func (r *reviewCmd) Run(c *cli) error {
	questions, err := c.questions()
	if err != nil {
		return err
	}
	if r.DumpQuestions {
		return printJSON(questions)
	}
	if r.URL == "" {
		return errors.New("expected a pull request url")
	}

	state, err := github.Gather(r.URL, c.gatherOptions(), c.logf)
	if err != nil {
		return err
	}
	if r.DumpState {
		return printJSON(state)
	}

	client, err := c.client()
	if err != nil {
		return err
	}
	c.logf("evaluating %d criteria", len(questions))
	resp, err := client.Evaluate(state, review.APIQuestions(questions))
	if err != nil {
		return err
	}

	if c.JSON {
		return printJSON(resp)
	}
	review.Report(os.Stdout, state.PR, state.DocName, questions, resp, r.Price)
	return nil
}

type evalCmd struct {
	Corpus string `arg:"" type:"existingfile" help:"Corpus JSON, e.g. eval/corpus.json."`
}

func (e *evalCmd) Run(c *cli) error {
	questions, err := c.questions()
	if err != nil {
		return err
	}
	corpus, err := eval.LoadCorpus(e.Corpus)
	if err != nil {
		return err
	}
	client, err := c.client()
	if err != nil {
		return err
	}

	results := eval.Run(client, questions, corpus, c.gatherOptions(), c.logf)
	if !eval.AnySucceeded(results) {
		return errors.New("all PRs failed, nothing to score")
	}
	if c.JSON {
		return printJSON(results)
	}
	eval.PrintAUCTable(os.Stdout, eval.AUCTable(results, questions))
	return nil
}

type completionsCmd struct {
	Shell string `arg:"" enum:"bash,fish,zsh" help:"bash, fish or zsh."`
}

func (s *completionsCmd) Run(ctx *kong.Context) error {
	script, err := completions(s.Shell, ctx.Model.Node)
	if err != nil {
		return err
	}
	fmt.Print(script)
	return nil
}

type versionCmd struct{}

func (versionCmd) Run() error {
	fmt.Printf("hunch %s\n", version())
	return nil
}

func (c *cli) logf(format string, a ...any) {
	if c.Verbose {
		log.Printf(format, a...)
	}
}

func (c *cli) questions() (map[string]review.Question, error) {
	if c.Questions == "" {
		return review.Default(), nil
	}
	return review.Load(c.Questions)
}

func (c *cli) gatherOptions() github.Options {
	return github.Options{Docs: c.Docs, MaxDiff: c.MaxDiff, MaxDoc: c.MaxDoc}
}

// client builds the TypeSafe client shared by review and eval.
func (c *cli) client() (*typesafe.Client, error) {
	key, err := apiKey(c.Key)
	if err != nil {
		return nil, err
	}
	return &typesafe.Client{
		APIKey:  key,
		Model:   c.Model,
		HTTP:    &http.Client{Timeout: 5 * time.Minute},
		Retries: c.Retries,
		Logf:    c.logf,
	}, nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// apiKey takes TYPESAFE_API_KEY, else the named key from skate.
func apiKey(name string) (string, error) {
	if key := os.Getenv("TYPESAFE_API_KEY"); key != "" {
		return key, nil
	}
	out, err := exec.Command("skate", "get", name).Output()
	if key := strings.TrimSpace(string(out)); err == nil && key != "" {
		return key, nil
	}
	return "", fmt.Errorf("no API key: set TYPESAFE_API_KEY or store one with `skate set %s <key>`", name)
}
