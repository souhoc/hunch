package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/alecthomas/kong"
)

var supportedShells = []string{"bash", "fish", "zsh"}

type cliFlag struct {
	Name      string
	Short     rune // 0 when the flag has no short form
	Usage     string
	Bool      bool // boolean flags take no argument
	TakesFile bool
}

// cliFlags reads the flags actually defined on the kong model — the root's and
// the default review command's, the ones a real invocation types — so
// completions cannot drift from them. Subcommands are deliberately absent: the
// `completions` one in particular is not something you want offered while
// completing a real invocation.
func cliFlags(root *kong.Node) []cliFlag {
	flags := root.Flags
	if root.DefaultCmd != nil {
		flags = append(flags[:len(flags):len(flags)], root.DefaultCmd.Flags...)
	}

	var out []cliFlag
	for _, f := range flags {
		if f.Hidden {
			continue
		}
		t := f.Tag.Type
		out = append(out, cliFlag{
			Name:      f.Name,
			Short:     f.Short,
			Usage:     sanitise(f.Help),
			Bool:      f.IsBool(),
			TakesFile: t == "existingfile" || t == "path",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// sanitise removes the characters that would terminate a description early in
// fish or zsh completion syntax.
func sanitise(usage string) string {
	usage, _, _ = strings.Cut(usage, "\n")
	return strings.NewReplacer("'", "", "[", "(", "]", ")").Replace(usage)
}

func completions(shell string, root *kong.Node) (string, error) {
	switch shell {
	case "bash":
		return bashCompletions(root), nil
	case "fish":
		return fishCompletions(root), nil
	case "zsh":
		return zshCompletions(root), nil
	}
	return "", fmt.Errorf("unsupported shell %q: want %s", shell, strings.Join(supportedShells, ", "))
}

func fishCompletions(root *kong.Node) string {
	var b strings.Builder
	b.WriteString("# hunch completions for fish\n")
	b.WriteString("# install: hunch completions fish > ~/.config/fish/completions/hunch.fish\n\n")
	b.WriteString("complete -c hunch -f\n")
	for _, f := range cliFlags(root) {
		fmt.Fprintf(&b, "complete -c hunch -l %s", f.Name)
		if f.Short != 0 {
			fmt.Fprintf(&b, " -s %c", f.Short)
		}
		fmt.Fprintf(&b, " -d '%s'", f.Usage)
		if f.TakesFile {
			b.WriteString(" -r -F")
		} else if !f.Bool {
			b.WriteString(" -r")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func bashCompletions(root *kong.Node) string {
	var names, fileOpts []string
	for _, f := range cliFlags(root) {
		names = append(names, "--"+f.Name)
		if f.Short != 0 {
			names = append(names, "-"+string(f.Short))
		}
		if f.TakesFile {
			fileOpts = append(fileOpts, "--"+f.Name)
		}
	}

	return fmt.Sprintf(`# hunch completions for bash
# install: hunch completions bash > /usr/local/etc/bash_completion.d/hunch

_hunch() {
    local cur prev
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    case "$prev" in
        %s)
            COMPREPLY=( $(compgen -f -- "$cur") )
            return
            ;;
    esac

    if [[ "$cur" == -* ]]; then
        COMPREPLY=( $(compgen -W "%s" -- "$cur") )
        return
    fi
    COMPREPLY=()
}
complete -o default -F _hunch hunch
`, strings.Join(fileOpts, "|"), strings.Join(names, " "))
}

func zshCompletions(root *kong.Node) string {
	var b strings.Builder
	b.WriteString("#compdef hunch\n")
	b.WriteString("# install: hunch completions zsh > \"${fpath[1]}/_hunch\"\n\n")
	b.WriteString("_hunch() {\n    _arguments -s \\\n")
	for _, f := range cliFlags(root) {
		spec := "--" + f.Name
		if f.Short != 0 {
			spec = fmt.Sprintf("(-%c --%s)'{-%c,--%s}'", f.Short, f.Name, f.Short, f.Name)
		}
		switch {
		case f.Bool:
			fmt.Fprintf(&b, "        '%s[%s]' \\\n", spec, f.Usage)
		case f.TakesFile:
			fmt.Fprintf(&b, "        '%s[%s]:file:_files' \\\n", spec, f.Usage)
		default:
			fmt.Fprintf(&b, "        '%s[%s]:%s:' \\\n", spec, f.Usage, f.Name)
		}
	}
	b.WriteString("        '*:pull request url:'\n}\n\n_hunch \"$@\"\n")
	return b.String()
}
