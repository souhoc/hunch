package main

import (
	"flag"
	"fmt"
	"sort"
	"strings"
)

var supportedShells = []string{"bash", "fish", "zsh"}

// fileFlags take a path, so completion should offer files for them.
var fileFlags = map[string]bool{"questions": true}

type cliFlag struct {
	Name      string
	Usage     string
	Bool      bool // boolean flags take no argument
	TakesFile bool
}

// cliFlags reads the flags actually defined in main, so completions cannot
// drift from them. The `completions` subcommand is deliberately absent: it is
// not something you want offered while completing a real invocation.
func cliFlags(fs *flag.FlagSet) []cliFlag {
	var out []cliFlag
	fs.VisitAll(func(f *flag.Flag) {
		b, ok := f.Value.(interface{ IsBoolFlag() bool })
		out = append(out, cliFlag{
			Name:      f.Name,
			Usage:     sanitise(f.Usage),
			Bool:      ok && b.IsBoolFlag(),
			TakesFile: fileFlags[f.Name],
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// sanitise removes the characters that would terminate a description early in
// fish or zsh completion syntax.
func sanitise(usage string) string {
	usage, _, _ = strings.Cut(usage, "\n")
	return strings.NewReplacer("'", "", "[", "(", "]", ")").Replace(usage)
}

func completions(shell string, fs *flag.FlagSet) (string, error) {
	switch shell {
	case "bash":
		return bashCompletions(fs), nil
	case "fish":
		return fishCompletions(fs), nil
	case "zsh":
		return zshCompletions(fs), nil
	}
	return "", fmt.Errorf("unsupported shell %q: want %s", shell, strings.Join(supportedShells, ", "))
}

func fishCompletions(fs *flag.FlagSet) string {
	var b strings.Builder
	b.WriteString("# hunch completions for fish\n")
	b.WriteString("# install: hunch completions fish > ~/.config/fish/completions/hunch.fish\n\n")
	b.WriteString("complete -c hunch -f\n")
	for _, f := range cliFlags(fs) {
		// -o matches the single dash Go flags are usually typed with, -l the double.
		fmt.Fprintf(&b, "complete -c hunch -o %s -l %s -d '%s'", f.Name, f.Name, f.Usage)
		if f.TakesFile {
			b.WriteString(" -r -F")
		} else if !f.Bool {
			// -x is -r plus "not a file"; plain -r still lists the directory.
			b.WriteString(" -x")
		}
		b.WriteString("\n")
	}
	// The subcommand is not offered, but its argument is worth completing.
	fmt.Fprintf(&b, "\ncomplete -c hunch -n '__fish_seen_subcommand_from completions' -x -a '%s'\n",
		strings.Join(supportedShells, " "))
	return b.String()
}

func bashCompletions(fs *flag.FlagSet) string {
	var names, fileOpts []string
	for _, f := range cliFlags(fs) {
		names = append(names, "-"+f.Name, "--"+f.Name)
		if f.TakesFile {
			fileOpts = append(fileOpts, "-"+f.Name, "--"+f.Name)
		}
	}

	var valueOpts []string
	for _, f := range cliFlags(fs) {
		if !f.Bool && !f.TakesFile {
			valueOpts = append(valueOpts, "-"+f.Name, "--"+f.Name)
		}
	}

	return fmt.Sprintf(`# hunch completions for bash
# install: hunch completions bash > /usr/local/etc/bash_completion.d/hunch

_hunch() {
    local cur prev
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    if [[ "${COMP_WORDS[1]}" == "completions" && $COMP_CWORD -eq 2 ]]; then
        COMPREPLY=( $(compgen -W "%s" -- "$cur") )
        return
    fi

    case "$prev" in
        %s)
            COMPREPLY=( $(compgen -f -- "$cur") )
            return
            ;;
        %s)
            COMPREPLY=()
            return
            ;;
    esac

    if [[ "$cur" == -* ]]; then
        COMPREPLY=( $(compgen -W "%s" -- "$cur") )
        return
    fi
    COMPREPLY=()
}
complete -F _hunch hunch
`, strings.Join(supportedShells, " "), strings.Join(fileOpts, "|"),
		strings.Join(valueOpts, "|"), strings.Join(names, " "))
}

func zshCompletions(fs *flag.FlagSet) string {
	var b strings.Builder
	b.WriteString("#compdef hunch\n")
	b.WriteString("# install: hunch completions zsh > \"${fpath[1]}/_hunch\"\n\n")
	b.WriteString("_hunch() {\n")
	fmt.Fprintf(&b, "    if [[ ${words[2]} == completions ]]; then\n        _values shell %s\n        return\n    fi\n\n",
		strings.Join(supportedShells, " "))
	b.WriteString("    _arguments -s \\\n")
	for _, f := range cliFlags(fs) {
		switch {
		case f.Bool:
			fmt.Fprintf(&b, "        '-%s[%s]' \\\n", f.Name, f.Usage)
		case f.TakesFile:
			fmt.Fprintf(&b, "        '-%s[%s]:file:_files' \\\n", f.Name, f.Usage)
		default:
			fmt.Fprintf(&b, "        '-%s[%s]:%s:' \\\n", f.Name, f.Usage, f.Name)
		}
	}
	b.WriteString("        '*:pull request url:'\n}\n\n_hunch \"$@\"\n")
	return b.String()
}
