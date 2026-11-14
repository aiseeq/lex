package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiseeq/lex/internal/lex"
)

// headerFixture builds a file with the lex header on and returns the codex, the file and its built text.
func headerFixture(t *testing.T) (string, string, []byte) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	codex := filepath.Join(root, "codex")
	output := filepath.Join(root, "garden.md")
	if err := os.WriteFile(output, []byte("# Garden\n\nWater the beds at dawn.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := lex.ImportWithOptions(codex, output, lex.ImportOptions{Home: root}); err != nil {
		t.Fatalf("import fixture: %v", err)
	}
	manifests, err := filepath.Glob(filepath.Join(codex, "manifest", "*.yaml"))
	if err != nil || len(manifests) != 1 {
		t.Fatalf("find manifest: %v, %v", manifests, err)
	}
	data, err := os.ReadFile(manifests[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifests[0], []byte(strings.Replace(string(data), "header: false\n", "header: true\n", 1)), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = captureRun(t, "build", "--codex", codex, "--force", output); err != nil {
		t.Fatalf("build fixture: %v", err)
	}
	built, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(built), "<!-- lex: собран из ") {
		t.Fatalf("fixture has no header: %q", built)
	}
	return codex, output, built
}

func withoutFirstLine(t *testing.T, data []byte) []byte {
	t.Helper()
	_, rest, ok := strings.Cut(string(data), "\n")
	if !ok {
		t.Fatalf("no first line in %q", data)
	}
	return []byte(rest)
}

func TestCheckHintRestoresDeletedHeader(t *testing.T) {
	codex, output, built := headerFixture(t)
	if err := os.WriteFile(output, withoutFirstLine(t, built), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := captureRun(t, "check", "--codex", codex)
	if err == nil || !strings.Contains(err.Error(), "lex absorb") {
		t.Fatalf("check of a file without its header should name lex absorb: %v", err)
	}
	if _, err = captureRun(t, "absorb", "--codex", codex, output); err != nil {
		t.Fatalf("absorb: %v", err)
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(built) {
		t.Errorf("absorb did not restore the built file:\n got %q\nwant %q", got, built)
	}
	if _, err = captureRun(t, "check", "--codex", codex); err != nil {
		t.Errorf("check after absorb: %v", err)
	}
	if stdout, err := captureRun(t, "status", "--codex", codex); err != nil {
		t.Errorf("status after absorb: %q, %v", stdout, err)
	}
}

func TestDeletedHeaderWithEditIsNotOverwritten(t *testing.T) {
	codex, output, built := headerFixture(t)
	edited := strings.Replace(string(withoutFirstLine(t, built)), "Water the beds at dawn.", "Water the beds at dusk.", 1)
	if err := os.WriteFile(output, []byte(edited), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "build", "--codex", codex, output); err == nil {
		t.Fatal("build of a file with a manual edit should refuse without --force")
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != edited {
		t.Errorf("refused build changed the file: %q", got)
	}
}

func TestAbsorbOfUnchangedHeaderFileWritesNothing(t *testing.T) {
	codex, output, _ := headerFixture(t)
	old := time.Date(2026, 3, 7, 8, 9, 10, 0, time.Local)
	if err := os.Chtimes(output, old, old); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := captureStreams(t, func() error { return run([]string{"absorb", "--codex", codex, output}) })
	if err != nil {
		t.Fatalf("absorb of an unchanged file: %v", err)
	}
	if stderr != "" {
		t.Errorf("absorb of an unchanged file printed %q", stderr)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(old) {
		t.Errorf("absorb of an unchanged file rewrote it at %v", info.ModTime())
	}
}
