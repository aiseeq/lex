package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAbsorbRepeatedPartRefusalNamesCopies(t *testing.T) {
	for _, tc := range []struct {
		name, difference string
		secondLine       string
		edit             func([]byte) []byte
	}{
		{name: "одна копия правлена", difference: `"- ONE"`, secondLine: "7", edit: func(b []byte) []byte { return bytes.Replace(b, []byte("- one"), []byte("- ONE"), 1) }},
		{name: "вставка вплотную после копии", difference: `"- new"`, secondLine: "8", edit: func(b []byte) []byte {
			b = bytes.ReplaceAll(b, []byte("- one"), []byte("- ONE"))
			return bytes.Replace(b, []byte("- ONE\n"), []byte("- ONE\n- new\n"), 1)
		}},
		{name: "пустые строки в конце", difference: "blank line", secondLine: "7", edit: func(b []byte) []byte {
			return append(append(bytes.ReplaceAll(b, []byte("- one"), []byte("- ONE")), '\n'), '\n')
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codex, out := setupRepeatedPartAdviceFixture(t, []byte("# T\n\n- one\n\n- two\n\n"))
			before := treeBytes(t, codex)
			file, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			file = tc.edit(file)
			mustWrite(t, out, file)
			before = treeBytes(t, codex)
			err = Absorb(codex, out, AbsorbOptions{})
			if err == nil {
				t.Fatal("Absorb accepted distinct edits")
			}
			message := err.Error()
			for _, want := range []string{"more than once", "line 3", "line " + tc.secondLine, tc.difference, "make every copy the same"} {
				if !strings.Contains(message, want) {
					t.Errorf("Absorb error %q does not contain %q", message, want)
				}
			}
			if !bytes.Equal(before, treeBytes(t, codex)) {
				t.Fatal("refusal changed codex")
			}
			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, file) {
				t.Fatal("refusal changed edited file")
			}
		})
	}
}

func setupRepeatedPartAdviceFixture(t *testing.T, original []byte) (string, string) {
	t.Helper()
	codex, out := t.TempDir(), filepath.Join(t.TempDir(), "guide.md")
	if err := importData(codex, out, "guide.md", original, false); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(codex, "manifest", "guide.md.yaml")
	m, err := readManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.parts) < 2 {
		t.Fatalf("imported %d parts, need at least 2", len(m.parts))
	}
	m.parts = append(m.parts, m.parts[1])
	if err := writeManifest(manifestPath, m, m.links); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	return codex, out
}
