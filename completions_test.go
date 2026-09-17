package main

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("hunch", flag.ContinueOnError)
	registerFlags(fs)
	return fs
}

// Every flag must be offered by every shell, or completion quietly goes stale.
func TestCompletionsCoverEveryFlag(t *testing.T) {
	fs := testFlagSet()
	var names []string
	fs.VisitAll(func(f *flag.Flag) { names = append(names, f.Name) })
	if len(names) < 10 {
		t.Fatalf("expected the real flag set, got %v", names)
	}

	// Each shell spells a flag differently; a bare substring match would also
	// accept "-questions" inside "-dump-questions".
	spelling := map[string]func(string) string{
		"bash": func(n string) string { return "-" + n + " --" + n },
		"fish": func(n string) string { return "-o " + n + " -l " + n + " " },
		"zsh":  func(n string) string { return "'-" + n + "[" },
	}

	for _, shell := range supportedShells {
		script, err := completions(shell, fs)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			if want := spelling[shell](name); !strings.Contains(script, want) {
				t.Errorf("%s completions omit %q", shell, want)
			}
		}
	}
}

// The subcommand must not be offered while completing a real invocation.
func TestCompletionsOmitTheSubcommand(t *testing.T) {
	for _, shell := range supportedShells {
		script, _ := completions(shell, testFlagSet())
		for _, line := range strings.Split(script, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue // install hints name it, that is fine
			}
			if strings.Contains(line, "completions") {
				t.Errorf("%s offers the subcommand: %s", shell, line)
			}
		}
	}
}

func TestFishMarksArgumentFlags(t *testing.T) {
	script := fishCompletions(testFlagSet())
	for _, tc := range []struct{ line, want string }{
		{"complete -c hunch -o json -l json", ""},              // bool: no argument
		{"complete -c hunch -o model -l model", " -r"},         // string: takes one
		{"complete -c hunch -o questions -l questions", " -F"}, // path: complete files
	} {
		var found string
		for _, line := range strings.Split(script, "\n") {
			if strings.HasPrefix(line, tc.line) {
				found = line
			}
		}
		if found == "" {
			t.Fatalf("no completion line for %q", tc.line)
		}
		if tc.want == "" && strings.HasSuffix(found, " -r") {
			t.Errorf("boolean flag asks for an argument: %s", found)
		}
		if tc.want != "" && !strings.Contains(found, tc.want) {
			t.Errorf("want %q in: %s", tc.want, found)
		}
	}
}

func TestCompletionsRejectUnknownShell(t *testing.T) {
	for _, shell := range []string{"", "ksh", "powershell"} {
		if _, err := completions(shell, testFlagSet()); err == nil {
			t.Errorf("%q was accepted", shell)
		} else if !strings.Contains(err.Error(), "bash, fish, zsh") {
			t.Errorf("error should list the shells, got: %v", err)
		}
	}
}

// A completion script that does not parse is worse than none.
func TestGeneratedScriptsParse(t *testing.T) {
	for _, tc := range []struct {
		shell, bin string
		args       []string
	}{
		{"bash", "bash", []string{"-n"}},
		{"zsh", "zsh", []string{"-n"}},
		{"fish", "fish", []string{"--no-execute"}},
	} {
		t.Run(tc.shell, func(t *testing.T) {
			bin, err := exec.LookPath(tc.bin)
			if err != nil {
				t.Skipf("%s not installed", tc.bin)
			}
			script, err := completions(tc.shell, testFlagSet())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "completion")
			if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(bin, append(tc.args, path)...).CombinedOutput(); err != nil {
				t.Errorf("%s rejected the script: %v\n%s", tc.shell, err, out)
			}
		})
	}
}

// Descriptions are interpolated into quoted shell strings.
func TestSanitise(t *testing.T) {
	for in, want := range map[string]string{
		"plain":         "plain",
		"don't":         "dont",
		"a [bracket]":   "a (bracket)",
		"first\nsecond": "first",
	} {
		if got := sanitise(in); got != want {
			t.Errorf("sanitise(%q) = %q, want %q", in, got, want)
		}
	}
}
