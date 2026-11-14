package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func moveFixture(t *testing.T, printed, header bool) (string, string, string, manifest) {
	t.Helper()
	codex, out, mp, m := purposeFixture(t, "- first\n\n- second\n\n- third\n")
	if printed {
		purposeSet(t, codex, true)
		setPurposeGoals(t, codex, mp, m, "reason first", "reason second", "reason third")
		m = mustReadManifest(t, mp)
	}
	if header {
		m.header = true
		if err := writeManifest(mp, m, m.links); err != nil {
			t.Fatal(err)
		}
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	return codex, out, mp, mustReadManifest(t, mp)
}

func TestAbsorbMoveKeepsFragmentHeader(t *testing.T) {
	codex, out, mp, m := moveFixture(t, false, false)
	if len(m.parts) < 3 {
		t.Fatalf("parts = %v", m.parts)
	}
	moved := m.parts[2]
	before, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(moved)+".md"))
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("- third\n\n- first\n\n- second\n")
	mustWrite(t, out, want)
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		t.Fatalf("Absorb: %v", err)
	}
	updated := mustReadManifest(t, mp)
	if len(updated.parts) < 3 {
		t.Fatalf("parts = %v", updated.parts)
	}
	if updated.parts[0] != moved {
		t.Errorf("first part = %q, want preserved id %q", updated.parts[0], moved)
	}
	after, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(moved)+".md"))
	if err != nil {
		t.Fatal(err)
	}
	beforeHeader := before[:bytes.Index(before[4:], []byte("---\n"))+8]
	afterHeader := after[:bytes.Index(after[4:], []byte("---\n"))+8]
	if !bytes.Equal(beforeHeader, afterHeader) {
		t.Errorf("moved fragment header changed\nbefore %q\nafter %q", beforeHeader, afterHeader)
	}
	assertAbsorbedOutputEquals(t, codex, out, want)
}

func TestAbsorbMoveWithPrintedPurpose(t *testing.T) {
	codex, out, mp, m := moveFixture(t, true, false)
	if len(m.parts) < 3 {
		t.Fatalf("parts = %v", m.parts)
	}
	moved := m.parts[2]
	want := []byte("- third\n  Зачем: reason third\n\n- first\n  Зачем: reason first\n\n- second\n  Зачем: reason second\n")
	mustWrite(t, out, want)
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		t.Fatalf("Absorb: %v", err)
	}
	updated := mustReadManifest(t, mp)
	if len(updated.parts) < 3 {
		t.Fatalf("parts = %v", updated.parts)
	}
	if updated.parts[0] != moved {
		t.Errorf("first part = %q, want %q", updated.parts[0], moved)
	}
	assertAbsorbedOutputEquals(t, codex, out, want)
}

func TestAbsorbDuplicatePartWithPrintedPurpose(t *testing.T) {
	codex, out, mp, m := moveFixture(t, true, false)
	if len(m.parts) < 2 {
		t.Fatalf("parts = %v", m.parts)
	}
	m.parts = append(m.parts, m.parts[0])
	if err := writeManifest(mp, m, m.links); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// Edit a different unique part while the first part has a second occurrence.
	want = bytes.Replace(want, []byte("- second\n"), []byte("- second edited\n"), 1)
	mustWrite(t, out, want)
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		t.Fatalf("Absorb: %v", err)
	}
	assertAbsorbedOutputEquals(t, codex, out, want)
}

func TestAbsorbDraftKeepsHeader(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mismatch bool
	}{{"boundary", false}, {"rebuild", true}} {
		t.Run(tc.name, func(t *testing.T) {
			codex, out, _, _ := moveFixture(t, true, true)
			file, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if len(file) == 0 {
				t.Fatal("built output is empty")
			}
			if tc.mismatch {
				file = bytes.Replace(file, []byte("- first\n"), []byte("- first changed\n"), 1)
				mustWrite(t, out, file)
				file, err = os.ReadFile(out)
				if err != nil {
					t.Fatal(err)
				}
				original := absorbReplaceFragmentBody
				absorbReplaceFragmentBody = func(file, old, repl []byte) []byte {
					return original(file, old, append(append([]byte(nil), repl...), []byte("corrupted\n")...))
				}
				defer func() { absorbReplaceFragmentBody = original }()
			} else {
				// Use adjacent heading and body fragments so the changed boundary is ambiguous.
				boundaryCodex := t.TempDir()
				boundaryOut := filepath.Join(t.TempDir(), "out.md")
				if err := importData(boundaryCodex, boundaryOut, "boundary.md", []byte("# One\na\n\n# Two\nb\n"), false); err != nil {
					t.Fatal(err)
				}
				boundaryManifest := filepath.Join(boundaryCodex, "manifest", "boundary.md.yaml")
				bm := mustReadManifest(t, boundaryManifest)
				bm.header = true
				if err := writeManifest(boundaryManifest, bm, bm.links); err != nil {
					t.Fatal(err)
				}
				if err := Build(boundaryCodex, false); err != nil {
					t.Fatal(err)
				}
				file, err = os.ReadFile(boundaryOut)
				if err != nil {
					t.Fatal(err)
				}
				file = bytes.Replace(file, []byte("# One\na\n\n# Two\nb\n"), []byte("# One\na\nX\nTWO\nb\n"), 1)
				mustWrite(t, boundaryOut, file)
				codex, out = boundaryCodex, boundaryOut
			}
			mustWrite(t, out, file)
			draftDir := filepath.Join(t.TempDir(), "draft-root")
			err = Absorb(codex, out, AbsorbOptions{DraftDir: draftDir})
			if err == nil || tc.mismatch && !strings.Contains(err.Error(), "absorbed fragments would not rebuild") {
				t.Fatalf("Absorb error = %v", err)
			}
			drafts, err := filepath.Glob(filepath.Join(draftDir, "drafts", "*.md"))
			if err != nil {
				t.Fatal(err)
			}
			if len(drafts) != 1 {
				t.Fatalf("drafts = %v", drafts)
			}
			got, err := os.ReadFile(drafts[0])
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("draft differs from on-disk file\ndraft %q\nfile %q", got, want)
			}
		})
	}
}
