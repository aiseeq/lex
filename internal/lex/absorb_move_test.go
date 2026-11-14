package lex

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func moveFixture(t *testing.T, printed, header bool) (string, string, string, manifest) {
	t.Helper()
	codex, out, mp, m := purposeFixture(t, "- first\n\n- second\n\n- third\n")
	if len(m.parts) < 3 {
		t.Fatalf("fixture parts = %v, want at least three", m.parts)
	}
	setPurposeGoals(t, codex, mp, m, "reason first", "reason second", "reason third")
	m = mustReadManifest(t, mp)
	if printed {
		purposeSet(t, codex, true)
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
	beforeHeader := testFragmentHeader(t, before)
	afterHeader := testFragmentHeader(t, after)
	if !bytes.Equal(beforeHeader, afterHeader) {
		t.Errorf("moved fragment header changed\nbefore %q\nafter %q", beforeHeader, afterHeader)
	}
	if got := purposeValue(after); got != "reason third" {
		t.Errorf("moved fragment goal = %q, want %q", got, "reason third")
	}
	assertAbsorbedOutputEquals(t, codex, out, want)
}

func TestAbsorbMoveWithPrintedPurpose(t *testing.T) {
	codex, out, mp, m := moveFixture(t, true, false)
	if len(m.parts) < 3 {
		t.Fatalf("parts = %v", m.parts)
	}
	moved := m.parts[2]
	before, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(moved)+".md"))
	if err != nil {
		t.Fatal(err)
	}
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
	beforeHeader := testFragmentHeader(t, before)
	after, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(moved)+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeHeader, testFragmentHeader(t, after)) {
		t.Errorf("printed move changed fragment header: %q -> %q", beforeHeader, testFragmentHeader(t, after))
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
			if err == nil || tc.mismatch && !strings.Contains(err.Error(), "absorbed fragments would not rebuild") || !tc.mismatch && !strings.Contains(err.Error(), "boundary conflict") {
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

func testFragmentHeader(t *testing.T, raw []byte) []byte {
	t.Helper()
	if len(raw) < 4 || !bytes.HasPrefix(raw, []byte("---\n")) {
		t.Fatalf("fragment has no header: %q", raw)
	}
	end := bytes.Index(raw[4:], []byte("---\n"))
	if end < 0 {
		t.Fatalf("fragment header is unterminated: %q", raw)
	}
	return raw[:end+8]
}

func TestAbsorbMoveRecordsBuiltHash(t *testing.T) {
	codex, out, mp, m := moveFixture(t, false, false)
	if len(m.parts) < 3 {
		t.Fatalf("parts = %v", m.parts)
	}
	want := []byte("- third\n\n- first\n\n- second\n")
	mustWrite(t, out, want)
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		t.Fatalf("Absorb: %v", err)
	}
	updated := mustReadManifest(t, mp)
	file, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if updated.builtHash != sha(file) {
		t.Errorf("built hash = %q, want %q", updated.builtHash, sha(file))
	}
	fragmentPath := filepath.Join(codex, "frag", filepath.FromSlash(updated.parts[0])+".md")
	raw, err := os.ReadFile(fragmentPath)
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(raw, []byte("- third"), []byte("- third edited"), 1)
	if bytes.Equal(raw, changed) {
		t.Fatalf("could not edit moved fragment %q", updated.parts[0])
	}
	mustWrite(t, fragmentPath, changed)
	if err := BuildWithOptions(codex, BuildOptions{}); err != nil {
		t.Fatalf("BuildWithOptions after fragment edit: %v", err)
	}
}

func TestAbsorbMoveKeepsSharedFragment(t *testing.T) {
	codex := t.TempDir()
	outA, outB := filepath.Join(t.TempDir(), "a.md"), filepath.Join(t.TempDir(), "b.md")
	if err := importData(codex, outA, "a.md", []byte("first\n\nsecond\n\nthird\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := importData(codex, outB, "b.md", []byte("other\n\nthird\n"), false); err != nil {
		t.Fatal(err)
	}
	manifestA := filepath.Join(codex, "manifest", codexKey("a.md")+".yaml")
	manifestB := filepath.Join(codex, "manifest", codexKey("b.md")+".yaml")
	ma, mb := mustReadManifest(t, manifestA), mustReadManifest(t, manifestB)
	if len(ma.parts) < 3 || len(mb.parts) < 2 {
		t.Fatalf("parts: a=%v b=%v", ma.parts, mb.parts)
	}
	mb.parts[1] = ma.parts[2]
	if err := writeManifest(manifestB, mb, mb.links); err != nil {
		t.Fatal(err)
	}
	if err := BuildWithOptions(codex, BuildOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	beforeB, err := os.ReadFile(outB)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, outA, []byte("third\n\nfirst\n\nsecond\n"))
	err = Absorb(codex, outA, AbsorbOptions{})
	if err == nil || !strings.Contains(err.Error(), "--shared") {
		t.Fatalf("Absorb error = %v, want shared-fragment refusal", err)
	}
	afterB, err := os.ReadFile(outB)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeB, afterB) {
		t.Errorf("shared output changed: %q -> %q", beforeB, afterB)
	}
	mb = mustReadManifest(t, manifestB)
	rebuiltB, err := manifestBody(codex, mb)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rebuiltB, afterB) {
		t.Errorf("shared output differs from its fragments: built %q, fragments %q", afterB, rebuiltB)
	}
}

func TestAbsorbMoveWarnsAboutEdit(t *testing.T) {
	codex, out, _, m := moveFixture(t, false, false)
	if len(m.parts) < 3 {
		t.Fatalf("parts = %v", m.parts)
	}
	mustWrite(t, out, []byte("- third\n\n- first\n\n- second\n"))
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	absorbErr := Absorb(codex, out, AbsorbOptions{})
	os.Stderr = oldStderr
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	warning, readErr := io.ReadAll(r)
	_ = r.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if absorbErr != nil {
		t.Fatalf("Absorb: %v", absorbErr)
	}
	if !bytes.Contains(warning, []byte("absorb moved an edit into the fragments")) {
		t.Errorf("warning = %q", warning)
	}
}
