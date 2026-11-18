package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefusalNamesPathOnce(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	a := filepath.Join(root, "docs", "a.md")
	b := filepath.Join(root, "docs", "b.md")
	if err := os.MkdirAll(filepath.Dir(a), directoryMode); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, a, []byte("# A\r\n\r\nown before\r\n\r\nshared para\n\nown after\r\n"))
	mustWrite(t, b, []byte("# B\n\nshared para\n"))
	for _, file := range []string{a, b} {
		if err := ImportWithOptions(codex, file, ImportOptions{Home: root}); err != nil {
			t.Fatal(err)
		}
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	built, err := os.ReadFile(a)
	if err != nil {
		t.Fatal(err)
	}
	crlf := bytes.ReplaceAll(bytes.ReplaceAll(built, []byte("\r\n"), []byte("\n")), []byte("\n"), []byte("\r\n"))
	mustWrite(t, a, crlf)

	_, checkErr := CheckWithWarnings(codex, CheckOptions{})
	if checkErr == nil {
		t.Fatal("check accepted a whole-file CRLF conversion")
	}
	checkRefusal := strings.SplitN(checkErr.Error(), "run `lex", 2)[0]
	if got := strings.Count(checkRefusal, a); got != 1 {
		t.Errorf("check refusal names output %d times, want once: %s", got, checkErr)
	}
	if !strings.Contains(checkErr.Error(), "line endings changed to CRLF") {
		t.Errorf("check refusal lost its explanation: %s", checkErr)
	}

	entries, err := StatusWithHome(codex, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Output != a {
			continue
		}
		line := entry.Output + "; " + entry.Refusal
		statusRefusal := strings.SplitN(line, "run `lex", 2)[0]
		if got := strings.Count(statusRefusal, a); got != 1 {
			t.Errorf("status refusal names output %d times, want once: %s", got, line)
		}
		if !strings.Contains(line, "line endings changed to CRLF") {
			t.Errorf("status refusal lost its explanation: %s", line)
		}
		return
	}
	t.Fatalf("status has no entry for %s", a)
}
