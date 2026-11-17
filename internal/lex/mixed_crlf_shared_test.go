package lex

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMixedCRLFSharedRefusal(t *testing.T) {
	cases := []struct {
		name       string
		a, b       []byte
		editNeedle string
	}{
		{
			name: "unchanged",
			a:    []byte("# A\r\n\r\nown before\r\n\r\nshared para\n\nown after\r\n"),
			b:    []byte("# B\n\nshared para\n"),
		},
		{
			name:       "ownedit",
			a:          []byte("# A\r\n\r\nown before\r\n\r\nshared para\n\nown after\r\n"),
			b:          []byte("# B\n\nshared para\n"),
			editNeedle: "own before",
		},
		{
			name:       "sharededit",
			a:          []byte("# A\r\n\r\nown before\r\n\r\nshared para\n\nown after\r\n"),
			b:          []byte("# B\n\nshared para\n"),
			editNeedle: "shared para",
		},
		{
			name: "sharedcrlf",
			a:    []byte("# A\n\nown before\n\nshared para\r\n\r\nown after\n"),
			b:    []byte("# B\r\n\r\nshared para\r\n"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			codex := filepath.Join(root, "codex")
			a, b := filepath.Join(root, "docs", "a.md"), filepath.Join(root, "docs", "b.md")
			if err := os.MkdirAll(filepath.Dir(a), directoryMode); err != nil {
				t.Fatal(err)
			}
			mustWrite(t, a, tc.a)
			mustWrite(t, b, tc.b)
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
			if tc.editNeedle != "" {
				crlf = bytes.Replace(crlf, []byte(tc.editNeedle), []byte(tc.editNeedle+" edited"), 1)
			}
			mustWrite(t, a, crlf)
			bBefore, err := os.ReadFile(b)
			if err != nil {
				t.Fatal(err)
			}
			codexBefore := treeBytes(t, codex)

			for _, operation := range []struct {
				name string
				run  func() error
			}{
				{name: "absorb", run: func() error { return Absorb(codex, a, AbsorbOptions{DraftDir: codex}) }},
				{name: "refresh", run: func() error {
					return ImportWithOptions(codex, a, ImportOptions{Refresh: true, Home: root})
				}},
			} {
				t.Run(operation.name, func(t *testing.T) {
					err := operation.run()
					assertMixedCRLFRefusal(t, err)
					assertMixedCRLFUnchanged(t, codexBefore, codex, crlf, a, bBefore, b, operation.name)
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
				assertMixedCRLFRefusal(t, errors.New(entry.Refusal+" "+entry.Command))
			}
			if !found {
				t.Fatalf("status has no entry for %s", a)
			}

			_, err = CheckWithWarnings(codex, CheckOptions{})
			assertMixedCRLFRefusal(t, err)
			assertMixedCRLFUnchanged(t, codexBefore, codex, crlf, a, bBefore, b, "status or check")
		})
	}
}

func assertMixedCRLFRefusal(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("got no refusal; want mixed-line-ending CRLF refusal")
	}
	message := err.Error()
	if index := strings.Index(message, "line endings changed to CRLF"); index >= 0 {
		message = message[index:]
	}
	for _, phrase := range []string{
		"line endings changed to CRLF",
		"the fragments mix LF and CRLF",
		"restore the previous line endings",
		"lex build --force --codex",
	} {
		if !strings.Contains(message, phrase) {
			t.Errorf("refusal text %q does not contain %q", message, phrase)
		}
	}
	if strings.Contains(message, "first delete only") {
		t.Errorf("refusal gives shared paragraph deletion advice: %q", message)
	}
}

func assertMixedCRLFUnchanged(t *testing.T, codexBefore []byte, codex string, aBytes []byte, a string, bBefore []byte, b string, operation string) {
	t.Helper()
	if !bytes.Equal(codexBefore, treeBytes(t, codex)) {
		t.Errorf("%s changed codex", operation)
	}
	if got, err := os.ReadFile(a); err != nil || !bytes.Equal(got, aBytes) {
		t.Errorf("%s changed a.md: got %q, err %v", operation, got, err)
	}
	if got, err := os.ReadFile(b); err != nil || !bytes.Equal(got, bBefore) {
		t.Errorf("%s changed b.md: got %q, err %v", operation, got, err)
	}
}
