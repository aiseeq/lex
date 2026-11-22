package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/aiseeq/lex/internal/lex"
)

const subUsage = "usage: lex sub --codex DIR [-n N] [--] OLD NEW FILE\n       lex sub --codex DIR -F SPEC FILE"

type subOptions struct {
	codex, spec string
	want        int
	wantSet     bool
	positional  []string
}

// parseSubArguments reads flags anywhere before "--", as the sub tool does: text such as "- list item" is OLD or NEW.
func parseSubArguments(args []string) (subOptions, bool, error) {
	opts := subOptions{want: 1}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			opts.positional = append(opts.positional, args[i+1:]...)
			break
		}
		name, value, hasValue := strings.Cut(arg, "=")
		switch name {
		case "-h", "--help":
			return opts, true, nil
		case "--codex", "-n", "-F":
			if !hasValue {
				i++
				if i >= len(args) || args[i] == "" {
					return opts, false, usage(fmt.Sprintf("%s requires a value\n%s", name, subUsage))
				}
				value = args[i]
			}
			switch name {
			case "--codex":
				opts.codex = value
			case "-F":
				opts.spec = value
			default:
				n, err := strconv.Atoi(value)
				if err != nil || n < 1 {
					return opts, false, usage(fmt.Sprintf("-n: expected match count must be an integer >= 1, got %q", value))
				}
				opts.want, opts.wantSet = n, true
			}
			continue
		}
		if looksLikeSubFlag(arg) {
			return opts, false, usage(fmt.Sprintf("unknown flag %s (use -- before OLD that starts with -)\n%s", arg, subUsage))
		}
		opts.positional = append(opts.positional, arg)
	}
	if opts.codex == "" {
		return opts, false, usage("--codex DIR is required\n" + subUsage)
	}
	if opts.spec != "" {
		if opts.wantSet || len(opts.positional) != 1 {
			return opts, false, usage(subUsage)
		}
	} else if len(opts.positional) != 3 {
		return opts, false, usage(subUsage)
	}
	return opts, false, nil
}

func subCommand(args []string) error {
	opts, help, err := parseSubArguments(args)
	if help {
		command, _ := findCommand("sub")
		fmt.Print(commandUsageText(command))
		return nil
	}
	if err != nil {
		return err
	}
	var edits []lex.SubEdit
	var file string
	if opts.spec != "" {
		file = opts.positional[0]
		var r io.Reader = os.Stdin
		if opts.spec != "-" {
			f, err := os.Open(opts.spec)
			if err != nil {
				return usage(fmt.Sprintf("read spec: %v", err))
			}
			defer f.Close()
			r = f
		}
		if edits, err = lex.ParseSubSpec(r, file); err != nil {
			return usage(err.Error())
		}
	} else {
		file = opts.positional[2]
		edits = []lex.SubEdit{{Old: opts.positional[0], New: opts.positional[1], Want: opts.want, Origin: "arguments"}}
	}
	result, err := lex.Sub(opts.codex, file, edits)
	if err != nil {
		return fmt.Errorf("%w\n%s", err, subFallback(opts, file))
	}
	for _, path := range result.Shared {
		fmt.Println("shared fragment: also rebuilt " + path)
	}
	if len(result.Changed) == 0 {
		// As the sub tool: a replacement that leaves the text as it was is a success, and nothing is written.
		fmt.Println("lex sub: nothing changed: the replacement leaves " + file + " as it is")
		return nil
	}
	printCodexChangeHint(opts.codex, result.Changed)
	return nil
}

// looksLikeSubFlag is the sub tool's test: -x, --word, --word=…; "- list item" or "-1" is text.
func looksLikeSubFlag(arg string) bool {
	rest := strings.TrimLeft(arg, "-")
	if dashes := len(arg) - len(rest); dashes < 1 || dashes > 2 {
		return false
	}
	name, _, _ := strings.Cut(rest, "=")
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-') {
			return false
		}
	}
	return true
}

// subFallback is the one-line fallback after a refusal: the same edit straight in the file, then absorb.
func subFallback(opts subOptions, file string) string {
	edit := "sub -F " + shellWord(opts.spec) + " " + shellWord(file)
	if opts.spec == "-" {
		edit = "sub -F - " + shellWord(file) + " (the same spec on stdin)"
	}
	if opts.spec == "" {
		count := ""
		if opts.want != 1 {
			count = "-n " + strconv.Itoa(opts.want) + " "
		}
		edit = "sub " + count + "-- " + subShellArg(opts.positional[0]) + " " + subShellArg(opts.positional[1]) + " " + shellWord(file)
	}
	return "fallback: edit the file, then carry the edit into fragments: " + edit + " && lex absorb --codex " + shellWord(opts.codex) + " " + shellWord(file)
}

// subShellArg quotes an argument for a one-line bash command: text with line breaks or other control characters
// goes into $'…', where bash reads \n, \r, \t and \xHH back as the bytes.
func subShellArg(arg string) string {
	if !strings.ContainsFunc(arg, unicode.IsControl) {
		return lex.ShellQuoteArg(arg)
	}
	var b strings.Builder
	b.WriteString("$'")
	for _, r := range arg {
		switch {
		case r == '\\' || r == '\'':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}
