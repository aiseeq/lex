package lex

import (
	"strings"
	"testing"
)

// A byte order mark right before the lex header hides the header from the
// header checks: absorb and import --refresh refuse the file with advice to
// delete the mark instead of moving the header line into a fragment, and
// status and check give the same refusal.
func TestBOMBeforeHeaderRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(dir, codex, out string) error
	}{
		{"absorb", func(_, codex, out string) error { return Absorb(codex, out, AbsorbOptions{}) }},
		{"refresh", func(dir, codex, out string) error {
			return ImportWithOptions(codex, out, ImportOptions{Refresh: true, Home: dir})
		}},
		{"check", func(_, codex, out string) error { _, err := CheckWithWarnings(codex, CheckOptions{}); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, out, codex, _, data := builtWithHeader(t, "# A\n\n- one\n")
			mustWrite(t, out, append([]byte("\xef\xbb\xbf"), data...))
			before, err := snapshotCodexFiles(codex)
			if err != nil {
				t.Fatal(err)
			}
			err = tc.run(dir, codex, out)
			if err == nil || !strings.Contains(err.Error(), "byte order mark") {
				t.Fatalf("%s = %v, want a refusal naming the byte order mark", tc.name, err)
			}
			after, err := snapshotCodexFiles(codex)
			if err != nil {
				t.Fatal(err)
			}
			if changed := changedCodexFiles(before, after); len(changed) > 0 {
				t.Fatalf("%s changed the codex: %v", tc.name, changed)
			}
		})
	}
	t.Run("status", func(t *testing.T) {
		_, out, codex, _, data := builtWithHeader(t, "# A\n\n- one\n")
		mustWrite(t, out, append([]byte("\xef\xbb\xbf"), data...))
		status, err := Status(codex)
		if err != nil {
			t.Fatal(err)
		}
		if len(status) != 1 || !strings.Contains(status[0].Refusal, "byte order mark") || status[0].Command != "" {
			t.Fatalf("status = %#v, want a byte order mark refusal without a command", status)
		}
	})
}

// A byte order mark before front matter hides the front matter and the header
// after it; absorb names the mark rather than text above the header.
func TestBOMBeforeFrontMatterNamesMark(t *testing.T) {
	_, out, codex, _, data := builtWithHeader(t, "---\nname: a\n---\n# A\n\n- one\n")
	mustWrite(t, out, append([]byte("\xef\xbb\xbf"), data...))
	err := Absorb(codex, out, AbsorbOptions{})
	if err == nil || !strings.Contains(err.Error(), "byte order mark") || strings.Contains(err.Error(), "text above lex header") {
		t.Fatalf("absorb = %v, want the byte order mark refusal", err)
	}
}
