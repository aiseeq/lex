package main

import (
	"os/exec"
	"testing"
)

// The git add line names plain paths bare, as before, and quotes the rest so a shell reads them back.
func TestShellWordReadsBack(t *testing.T) {
	for _, path := range []string{"/x/frag/.claude-a%2Db/one-1.md", "/x/правила.md"} {
		if got := shellWord(path); got != path {
			t.Errorf("shellWord(%q) = %q, want it bare", path, got)
		}
	}
	for _, path := range []string{"/x/a $HOME b/f.md", "/x/`t`.md", "/x/a'b.md", "/x/~a.md", "/x/*.md", ""} {
		out, err := exec.Command("sh", "-c", `printf '%s' `+shellWord(path)).Output()
		if err != nil {
			t.Fatalf("sh for %q: %v", path, err)
		}
		if string(out) != path {
			t.Errorf("shellWord(%q) = %s reads back as %q", path, shellWord(path), out)
		}
	}
}
