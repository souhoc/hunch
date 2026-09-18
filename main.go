package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("hunch: ")

	o := registerFlags(flag.CommandLine)
	flag.Parse()

	switch flag.Arg(0) {
	case "completions":
		script, err := completions(flag.Arg(1), flag.CommandLine)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(script)
		return
	case "version":
		fmt.Printf("hunch %s\n", version())
		return
	}

	logf := func(format string, a ...any) {}
	if o.verbose {
		logf = func(format string, a ...any) { log.Printf(format, a...) }
	}

	questions := defaultQuestions()
	if o.qFile != "" {
		var err error
		if questions, err = loadQuestions(o.qFile); err != nil {
			log.Fatal(err)
		}
	}
	if o.dumpQ {
		printJSON(questions)
		return
	}

	if flag.Arg(0) == "eval" {
		if flag.NArg() < 2 {
			fmt.Fprintln(os.Stderr, "usage: hunch eval <corpus.json>")
			os.Exit(2)
		}
		corpus, err := loadCorpus(flag.Arg(1))
		if err != nil {
			log.Fatal(err)
		}
		apiKey, err := apiKey(o.keyName)
		if err != nil {
			log.Fatal(err)
		}
		c := newClient(o, apiKey, logf)
		results := runEval(c, questions, corpus, o, logf)
		if !anySucceeded(results) {
			log.Fatal("all PRs failed, nothing to score")
		}
		if o.asJSON {
			printJSON(results)
			return
		}
		printAUCTable(os.Stdout, aucTable(results, questions))
		return
	}

	if flag.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "usage: hunch [flags] <pull request url>\n")
		fmt.Fprintf(os.Stderr, "       hunch completions %s\n", strings.Join(supportedShells, "|"))
		fmt.Fprintf(os.Stderr, "       hunch version\n")
		fmt.Fprintf(os.Stderr, "       hunch eval <corpus.json>\n\n")
		flag.PrintDefaults()
		os.Exit(2)
	}
	url := flag.Arg(0)

	logf("fetching pull request")
	pr, err := fetchPR(url)
	if err != nil {
		log.Fatal(err)
	}

	logf("fetching diff")
	diff, err := fetchDiff(url, o.maxDiff)
	if err != nil {
		log.Fatal(err)
	}

	docName, guidelines := fetchGuidelines(pr, strings.Split(o.docNames, ","), o.maxDoc)
	if docName == "" {
		logf("no project doc found (%s)", o.docNames)
	} else {
		logf("using %s as project guidelines", docName)
	}

	state := State{PR: pr, Guidelines: guidelines, Diff: diff}
	if o.dumpState {
		printJSON(state)
		return
	}

	apiKey, err := apiKey(o.keyName)
	if err != nil {
		log.Fatal(err)
	}

	logf("evaluating %d criteria", len(questions))
	c := newClient(o, apiKey, logf)
	resp, err := c.evaluate(state, apiQuestions(questions))
	if err != nil {
		log.Fatal(err)
	}

	if o.asJSON {
		printJSON(resp)
		return
	}
	report(os.Stdout, pr, docName, questions, resp, o.price)
}

// newClient builds the TypeSafe client shared by a normal run and `hunch eval`.
func newClient(o *options, apiKey string, logf func(format string, a ...any)) *client {
	return &client{
		apiKey:  apiKey,
		model:   o.model,
		http:    &http.Client{Timeout: 5 * time.Minute},
		retries: o.retries,
		logf:    logf,
	}
}

func loadQuestions(path string) (map[string]Question, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var q map[string]Question
	if err := json.Unmarshal(raw, &q); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(q) == 0 {
		return nil, fmt.Errorf("%s: no questions", path)
	}
	return q, nil
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		log.Fatal(err)
	}
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

// options holds every command-line flag. Registering them on a FlagSet rather
// than the package-level flag functions keeps them introspectable, which is how
// completions.go stays in step with them.
type options struct {
	model     string
	qFile     string
	docNames  string
	keyName   string
	maxDiff   int
	maxDoc    int
	retries   int
	price     float64
	dumpQ     bool
	dumpState bool
	asJSON    bool
	verbose   bool
}

func registerFlags(fs *flag.FlagSet) *options {
	o := &options{}
	fs.StringVar(&o.model, "model", "jev-latest", "TypeSafe model")
	fs.StringVar(&o.qFile, "questions", "", "JSON file overriding the default criteria")
	fs.StringVar(&o.docNames, "docs", "CLAUDE.md,README.md", "project docs to look for, first match wins")
	fs.StringVar(&o.keyName, "key", "typesafe:hunch", "skate key holding the API key")
	fs.IntVar(&o.maxDiff, "max-diff", 10000, "truncate the diff to this many bytes")
	fs.IntVar(&o.maxDoc, "max-doc", 30000, "truncate the project doc to this many bytes")
	fs.IntVar(&o.retries, "retries", 3, "retries on 429/529")
	fs.Float64Var(&o.price, "price", defaultPricePerMTok, "USD per million input tokens, for the cost line")
	fs.BoolVar(&o.dumpQ, "dump-questions", false, "print the default criteria as JSON and exit")
	fs.BoolVar(&o.dumpState, "dump-state", false, "print the state that would be sent and exit (no API call)")
	fs.BoolVar(&o.asJSON, "json", false, "print the raw API response")
	fs.BoolVar(&o.verbose, "v", false, "log progress to stderr")
	return o
}
