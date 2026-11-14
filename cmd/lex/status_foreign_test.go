package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiseeq/lex/internal/lex"
)

// foreignHeaderFixture builds a file without a header and then writes another codex's header into it by hand.
func foreignHeaderFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	codex := filepath.Join(root, "codex")
	output := filepath.Join(root, "notes.md")
	if err := os.WriteFile(output, []byte("# Notes\n\nKeep the seeds dry.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := lex.ImportWithOptions(codex, output, lex.ImportOptions{Home: root}); err != nil {
		t.Fatalf("import fixture: %v", err)
	}
	if err := lex.Build(codex, false); err != nil {
		t.Fatalf("build fixture: %v", err)
	}
	header := "<!-- lex: собран из " + filepath.Join(root, "other", "manifest", "notes.md.yaml") + ". Правь фрагменты в " + filepath.Join(root, "other", "frag") + " и запусти `lex build`. -->\n"
	if err := os.WriteFile(output, []byte(header+"# Notes\n\nKeep the seeds dry.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return codex, output
}

func TestStatusForeignHeaderNamesNoRefusedCommand(t *testing.T) {
	codex, output := foreignHeaderFixture(t)
	stdout, err := captureRun(t, "status", "--codex", codex)
	if err == nil {
		t.Fatalf("status of a changed file should fail: %q", stdout)
	}
	if !strings.Contains(stdout, output) {
		t.Fatalf("status does not name the file: %q", stdout)
	}
	if !strings.Contains(stdout, "header of codex "+filepath.Join(filepath.Dir(codex), "other")) {
		t.Errorf("status does not name the codex of the header: %q", stdout)
	}
	_, command, found := strings.Cut(stdout, "run lex ")
	if !found {
		t.Fatalf("status names no command: %q", stdout)
	}
	fields := strings.Fields(command)
	if len(fields) < 5 || fields[0] == "import" || fields[0] == "absorb" {
		t.Fatalf("status names a command that refuses a file with another codex's header: %q", stdout)
	}
	args := []string{fields[0], fields[1], fields[2], fields[3]}
	if args[1] != "--force" || args[2] != "--codex" {
		t.Fatalf("unexpected command form %q", command)
	}
	args[3], fields[4] = strings.Trim(args[3], `"`), strings.Trim(fields[4], `"`)
	args = append(args, fields[4])
	if _, err := captureRun(t, args...); err != nil {
		t.Fatalf("the command status named fails: %v %v", args, err)
	}
	if stdout, err := captureRun(t, "status", "--codex", codex); err != nil {
		t.Errorf("status after the named command still differs: %q, %v", stdout, err)
	}
}

// TestForeignHeaderRefreshStillRefuses pins the refusals the status line names; build --force FILE is the one command that rebuilds such a file.
func TestForeignHeaderRefreshStillRefuses(t *testing.T) {
	codex, output := foreignHeaderFixture(t)
	for _, args := range [][]string{
		{"import", "--codex", codex, "--refresh", output},
		{"absorb", "--codex", codex, output},
		{"build", "--codex", codex},
	} {
		if _, err := captureRun(t, args...); err == nil || !strings.Contains(err.Error(), "output belongs to codex") {
			t.Errorf("%v: want the foreign-codex refusal, got %v", args, err)
		}
	}
}
