package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMixedCRLFSharedRefusal(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	a, b := filepath.Join(root, "docs", "a.md"), filepath.Join(root, "docs", "b.md")
	if err := os.MkdirAll(filepath.Dir(a), directoryMode); err != nil {
		t.Fatal(err)
	}
	mixed := []byte("# A\r\n\r\nown before\r\n\r\nshared para\n\nown after\r\n")
	mustWrite(t, a, mixed)
	mustWrite(t, b, []byte("# B\n\nshared para\n"))
	if err := ImportWithOptions(codex, a, ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, b, ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	built, err := os.ReadFile(a)
	if err != nil {
		t.Fatal(err)
	}
	if uniformLineEnding(absorbLines(built)) != lineEndingMixed {
		t.Fatalf("fixture output endings = %v, want mixed", uniformLineEnding(absorbLines(built)))
	}
	crlf := bytes.ReplaceAll(bytes.ReplaceAll(built, []byte("\r\n"), []byte("\n")), []byte("\n"), []byte("\r\n"))
	mustWrite(t, a, crlf)
	bBefore, err := os.ReadFile(b)
	if err != nil {
		t.Fatal(err)
	}
	codexBefore := treeBytes(t, codex)

	for name, run := range map[string]func() error{
		"absorb": func() error { return Absorb(codex, a, AbsorbOptions{DraftDir: codex}) },
		"refresh": func() error {
			return ImportWithOptions(codex, a, ImportOptions{Refresh: true, Home: root})
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := run()
			if err == nil || !strings.Contains(err.Error(), "CRLF") || !strings.Contains(err.Error(), "LF") || strings.Contains(err.Error(), "first delete only") {
				t.Fatalf("%s error = %v; want CRLF refusal without shared paragraph deletion advice", name, err)
			}
			if !bytes.Equal(codexBefore, treeBytes(t, codex)) {
				t.Fatalf("%s changed codex", name)
			}
			if got, err := os.ReadFile(a); err != nil || !bytes.Equal(got, crlf) {
				t.Fatalf("%s changed a.md: got %q, err %v", name, got, err)
			}
			if got, err := os.ReadFile(b); err != nil || !bytes.Equal(got, bBefore) {
				t.Fatalf("%s changed b.md: got %q, err %v", name, got, err)
			}
		})
	}

	entries, err := StatusWithHome(codex, root)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.Output != a {
			continue
		}
		found = true
		advice := entry.Refusal + " " + entry.Command
		if !strings.Contains(advice, "CRLF") || !strings.Contains(advice, "LF") || strings.Contains(advice, "first delete only") {
			t.Fatalf("status advice = %#v; want CRLF refusal without shared paragraph deletion advice", entry)
		}
	}
	if !found {
		t.Fatalf("status has no entry for %s", a)
	}

	_, err = CheckWithWarnings(codex, CheckOptions{})
	if err == nil || !strings.Contains(err.Error(), "CRLF") || !strings.Contains(err.Error(), "LF") || strings.Contains(err.Error(), "first delete only") {
		t.Fatalf("check advice = %v; want CRLF refusal without shared paragraph deletion advice", err)
	}
	if !bytes.Equal(codexBefore, treeBytes(t, codex)) {
		t.Fatal("status or check changed codex")
	}
}
