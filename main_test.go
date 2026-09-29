package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `hunch <url>` must keep working after the default command was renamed to pr.
func TestDefaultCommandIsPR(t *testing.T) {
	for args, want := range map[string]string{
		"https://github.com/o/r/pull/1":    "pr <url>",
		"pr https://github.com/o/r/pull/1": "pr <url>",
		"comment":                          "comment",
	} {
		p, err := newParser(&cli{})
		if err != nil {
			t.Fatal(err)
		}
		ctx, err := p.Parse(strings.Fields(args))
		if err != nil {
			t.Fatalf("%q: %v", args, err)
		}
		if got := ctx.Command(); got != want {
			t.Errorf("%q parsed as %q, want %q", args, got, want)
		}
	}
}

func TestReadCode(t *testing.T) {
	file := func(content string) *os.File {
		p := filepath.Join(t.TempDir(), "code")
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return f
	}

	code, err := readCode(file("func f() {}\n"))
	if err != nil || code != "func f() {}\n" {
		t.Errorf("got %q, %v", code, err)
	}

	if _, err := readCode(file(" \n\t")); err == nil || !strings.Contains(err.Error(), "nothing to judge") {
		t.Errorf("blank input: got %v", err)
	}

	// A character device stands in for a terminal: nothing was piped.
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	if _, err := readCode(null); err == nil || !strings.Contains(err.Error(), "no code on stdin") {
		t.Errorf("character device: got %v", err)
	}
}
