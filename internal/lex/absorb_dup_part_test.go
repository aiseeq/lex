package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAbsorbSameEditOfRepeatedPart(t *testing.T) {
	for _, tc := range []struct {
		name       string
		otherEdit  bool
		staleParts bool
		force      bool
	}{
		{name: "same edit of both copies"},
		{name: "same edit and another part", otherEdit: true},
		{name: "force with newer fragments", staleParts: true, force: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codex := t.TempDir()
			out := filepath.Join(t.TempDir(), "guide.md")
			original := []byte("# T\n\n- one\n\n- two\n\n")
			if err := importData(codex, out, "guide.md", original, false); err != nil {
				t.Fatal(err)
			}
			manifestPath := filepath.Join(codex, "manifest", "guide.md.yaml")
			m, err := readManifest(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			if len(m.parts) < 3 {
				t.Fatalf("imported %d parts, want at least 3", len(m.parts))
			}
			repeated := m.parts[1]
			m.parts = append(m.parts, repeated)
			if err := writeManifest(manifestPath, m, m.links); err != nil {
				t.Fatal(err)
			}
			if err := Build(codex, false); err != nil {
				t.Fatal(err)
			}

			if tc.staleParts {
				path := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md")
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				raw = bytes.Replace(raw, []byte("# T"), []byte("# T2"), 1)
				mustWrite(t, path, raw)
			}

			edited, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			edited = bytes.ReplaceAll(edited, []byte("- one"), []byte("- ONE"))
			if tc.otherEdit {
				edited = bytes.Replace(edited, []byte("- two"), []byte("- TWO"), 1)
			}
			mustWrite(t, out, edited)

			options := AbsorbOptions{Force: tc.force}
			if err := Absorb(codex, out, options); err != nil {
				t.Fatalf("Absorb: %v", err)
			}
			if err := Build(codex, false); err != nil {
				t.Fatalf("rebuild: %v", err)
			}
			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, edited) {
				t.Fatalf("rebuilt output = %q, want %q", got, edited)
			}
			if warnings, err := CheckWithWarnings(codex, CheckOptions{}); err != nil || len(warnings) != 0 {
				t.Fatalf("check after absorb = %v, warnings %v", err, warnings)
			}
		})
	}
}

func TestAbsorbRefusesTwoEditsOfOneRepeatedCopy(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "guide.md")
	original := []byte("# T\n\none\nmid\nmore\n\n- two\n\n")
	if err := importData(codex, out, "guide.md", original, false); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(codex, "manifest", "guide.md.yaml")
	m, err := readManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	m.parts = append(m.parts, m.parts[1])
	if err := writeManifest(manifestPath, m, m.links); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	built, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	edited := bytes.Replace(built, []byte("one\nmid\nmore\n"), []byte("ONE\nmid\nMORE\n"), 1)
	edited = bytes.Replace(edited, []byte("- two"), []byte("- TWO"), 1)
	mustWrite(t, out, edited)
	before := treeBytes(t, codex)
	err = Absorb(codex, out, AbsorbOptions{})
	if err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("Absorb = %v, want refusal for one edited copy of a repeated part", err)
	}
	if !bytes.Equal(before, treeBytes(t, codex)) {
		t.Fatal("refused absorb changed the codex")
	}
}
