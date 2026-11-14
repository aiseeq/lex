package lex

import (
	"os/exec"
	"testing"
)

// Every hint argument reads back from sh as the original bytes.
func TestShellQuoteArgReadsBack(t *testing.T) {
	for _, arg := range []string{"/x/plain.md", `/x/a "q".md`, "/x/a 'q'.md", "/x/$HOME.md", "/x/`t`.md", `/x/a\b.md`, "/x/bang!.md", "/x/\xff.md", "/x/tab\t.md"} {
		out, err := exec.Command("sh", "-c", `printf '%s' `+ShellQuoteArg(arg)).Output()
		if err != nil {
			t.Fatalf("sh for %q: %v", arg, err)
		}
		if string(out) != arg {
			t.Errorf("ShellQuoteArg(%q) = %s reads back as %q", arg, ShellQuoteArg(arg), out)
		}
	}
}
