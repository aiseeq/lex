package lex

import (
	"bytes"
	"path/filepath"
	"slices"
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

// Any invisible bytes that hide a lex header - two byte order marks, a mark
// before another codex's header or after front matter, a zero-width or plain
// space - get the refusal naming them, so absorb never moves the header line
// into a fragment and check never settles a file with two headers.
func TestInvisiblePrefixBeforeHeaderRefused(t *testing.T) {
	const fm = "---\nname: a\n---\n"
	for _, tc := range []struct {
		name, original, want string
		corrupt              func(data []byte, codex string) []byte
	}{
		{"two byte order marks", "# A\n\n- one\n", "2 byte order marks", func(data []byte, _ string) []byte {
			return append([]byte("\xef\xbb\xbf\xef\xbb\xbf"), data...)
		}},
		{"mark before a foreign header", "# A\n\n- one\n", "a byte order mark", func(data []byte, codex string) []byte {
			return append([]byte("\xef\xbb\xbf"), bytes.Replace(data, []byte(filepath.Base(codex)+"/manifest/"), []byte("other/manifest/"), 1)...)
		}},
		{"mark after front matter", fm + "# A\n\n- one\n", "start of line 4", func(data []byte, _ string) []byte {
			return slices.Concat([]byte(fm), []byte("\xef\xbb\xbf"), data[len(fm):])
		}},
		{"zero-width space", "# A\n\n- one\n", "invisible bytes E2 80 8B", func(data []byte, _ string) []byte {
			return append([]byte("​"), data...)
		}},
		{"plain space", "# A\n\n- one\n", "invisible bytes 20", func(data []byte, _ string) []byte {
			return append([]byte(" "), data...)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, out, codex, _, data := builtWithHeader(t, tc.original)
			corrupted := tc.corrupt(data, codex)
			if bytes.Equal(corrupted, data) {
				t.Fatal("the corruption did not change the file")
			}
			mustWrite(t, out, corrupted)
			before, err := snapshotCodexFiles(codex)
			if err != nil {
				t.Fatal(err)
			}
			for name, run := range map[string]func() error{
				"absorb": func() error { return Absorb(codex, out, AbsorbOptions{}) },
				"check":  func() error { _, err := CheckWithWarnings(codex, CheckOptions{}); return err },
			} {
				if err := run(); err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "header is not recognised") {
					t.Fatalf("%s = %v, want the refusal naming %q", name, err, tc.want)
				}
			}
			after, err := snapshotCodexFiles(codex)
			if err != nil {
				t.Fatal(err)
			}
			if changed := changedCodexFiles(before, after); len(changed) > 0 {
				t.Fatalf("absorb changed the codex: %v", changed)
			}
		})
	}
}

// A file built without a lex header may hold a marker line behind spaces as
// its own text - an indented code block quoting a header - and absorb takes
// an edit in it as before.
func TestIndentedMarkerWithoutHeaderAbsorbs(t *testing.T) {
	dir := t.TempDir()
	out, codex := filepath.Join(dir, "a.md"), filepath.Join(dir, "cx")
	original := "    <!-- lex: собран из ~/x/manifest/a.yaml. Правь фрагменты в ~/x/frag -->\n\n- one\n"
	mustWrite(t, out, []byte(original))
	if err := importData(codex, out, "a.md", []byte(original), false); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, out, []byte(strings.Replace(original, "- one", "- ONE", 1)))
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		t.Fatalf("absorb = %v, want the edit taken", err)
	}
	if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
		t.Fatalf("check after absorb = %v", err)
	}
}
