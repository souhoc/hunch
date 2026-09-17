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

// The subcommand must never be offered as a candidate. Detecting it in order to
// complete its argument is fine — what matters is what the shell puts on screen.
func TestCompletionsOmitTheSubcommand(t *testing.T) {
	for _, cmdline := range []string{"hunch ", "hunch c", "hunch -"} {
		for _, got := range fishComplete(t, cmdline) {
			if got == "completions" {
				t.Errorf("%q offered the subcommand", cmdline)
			}
		}
	}

	// bash and zsh cannot be queried as cheaply; check their candidate lists.
	bash := bashCompletions(testFlagSet())
	for _, line := range strings.Split(bash, "\n") {
		if strings.Contains(line, "compgen -W") && strings.Contains(line, `"completions`) {
			t.Errorf("bash offers the subcommand: %s", line)
		}
	}
	zsh := zshCompletions(testFlagSet())
	if strings.Contains(zsh, "'1:command:") || strings.Contains(zsh, "(completions)") {
		t.Error("zsh offers the subcommand as a candidate")
	}
}

func TestFishMarksArgumentFlags(t *testing.T) {
	script := fishCompletions(testFlagSet())
	for _, tc := range []struct{ line, want string }{
		{"complete -c hunch -o json -l json", ""},              // bool: no argument
		{"complete -c hunch -o model -l model", " -x"},         // string: takes one, not a file
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

// fishComplete asks a real fish what it would offer for a command line.
func fishComplete(t *testing.T, cmdline string) []string {
	t.Helper()
	bin, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish not installed")
	}
	script := filepath.Join(t.TempDir(), "hunch.fish")
	body, err := completions("fish", testFlagSet())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(bin, "-c", "source "+script+"; complete -C '"+cmdline+"'").Output()
	if err != nil {
		t.Fatalf("fish failed: %v", err)
	}
	var got []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			got = append(got, strings.SplitN(line, "\t", 2)[0])
		}
	}
	return got
}

// A flag that takes a value must not offer the local directory listing. In fish
// that needs -x; -r alone means "takes an argument" and still completes files.
func TestFishValueFlagsDoNotCompleteFiles(t *testing.T) {
	for _, cmdline := range []string{"hunch -model ", "hunch -max-diff ", "hunch -retries "} {
		got := fishComplete(t, cmdline)
		for _, c := range got {
			if strings.HasSuffix(c, ".go") || strings.HasSuffix(c, ".md") {
				t.Errorf("%q offered files: %v", cmdline, got)
				break
			}
		}
	}
}

func TestFishCompletesFilesForPathFlags(t *testing.T) {
	if got := fishComplete(t, "hunch -questions "); len(got) == 0 {
		t.Error("-questions should complete files, got nothing")
	}
}

// The subcommand's own argument is worth completing even though the subcommand
// itself is deliberately not offered.
func TestFishCompletesShellNames(t *testing.T) {
	got := fishComplete(t, "hunch completions ")
	want := map[string]bool{"bash": false, "fish": false, "zsh": false}
	for _, c := range got {
		if _, ok := want[c]; ok {
			want[c] = true
		}
	}
	for shell, seen := range want {
		if !seen {
			t.Errorf("%q not offered after `hunch completions `, got %v", shell, got)
		}
	}
}

func TestFishStillOffersFlags(t *testing.T) {
	got := fishComplete(t, "hunch -")
	if len(got) < 12 {
		t.Errorf("expected every flag in both dash forms, got %d: %v", len(got), got)
	}
}
