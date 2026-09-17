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

	var (
		model     = flag.String("model", "jev-latest", "TypeSafe model")
		qFile     = flag.String("questions", "", "JSON file overriding the default criteria")
		docNames  = flag.String("docs", "CLAUDE.md,README.md", "project docs to look for, first match wins")
		maxDiff   = flag.Int("max-diff", 10000, "truncate the diff to this many bytes")
		maxDoc    = flag.Int("max-doc", 30000, "truncate the project doc to this many bytes")
		retries   = flag.Int("retries", 3, "retries on 429/529")
		price     = flag.Float64("price", defaultPricePerMTok, "USD per million input tokens, for the cost line")
		dumpQ     = flag.Bool("dump-questions", false, "print the default criteria as JSON and exit")
		dumpState = flag.Bool("dump-state", false, "print the state that would be sent and exit (no API call)")
		asJSON    = flag.Bool("json", false, "print the raw API response")
		keyName   = flag.String("key", "typesafe:hunch", "skate key holding the API key")
		verbose   = flag.Bool("v", false, "log progress to stderr")
	)
	flag.Parse()

	questions := defaultQuestions()
	if *qFile != "" {
		var err error
		if questions, err = loadQuestions(*qFile); err != nil {
			log.Fatal(err)
		}
	}
	if *dumpQ {
		printJSON(questions)
		return
	}

	if flag.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "usage: hunch [flags] <pull request url>\n\n")
		flag.PrintDefaults()
		os.Exit(2)
	}
	url := flag.Arg(0)

	logf := func(format string, a ...any) {}
	if *verbose {
		logf = func(format string, a ...any) { log.Printf(format, a...) }
	}

	logf("fetching pull request")
	pr, err := fetchPR(url)
	if err != nil {
		log.Fatal(err)
	}

	logf("fetching diff")
	diff, err := fetchDiff(url, *maxDiff)
	if err != nil {
		log.Fatal(err)
	}

	docName, guidelines := fetchGuidelines(pr, strings.Split(*docNames, ","), *maxDoc)
	if docName == "" {
		logf("no project doc found (%s)", *docNames)
	} else {
		logf("using %s as project guidelines", docName)
	}

	state := State{PR: pr, Guidelines: guidelines, Diff: diff}
	if *dumpState {
		printJSON(state)
		return
	}

	apiKey, err := apiKey(*keyName)
	if err != nil {
		log.Fatal(err)
	}

	logf("evaluating %d criteria", len(questions))
	c := &client{
		apiKey:  apiKey,
		model:   *model,
		http:    &http.Client{Timeout: 5 * time.Minute},
		retries: *retries,
		logf:    logf,
	}
	resp, err := c.evaluate(state, apiQuestions(questions))
	if err != nil {
		log.Fatal(err)
	}

	if *asJSON {
		printJSON(resp)
		return
	}
	report(pr, docName, questions, resp, *price)
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
