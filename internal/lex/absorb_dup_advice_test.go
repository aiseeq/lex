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
		name, difference      string
		firstLine, secondLine string
		original              []byte
		header                bool
		edit                  func([]byte) []byte
	}{
		{name: "одна копия правлена", difference: `"- ONE"`, firstLine: "3", secondLine: "7", edit: func(b []byte) []byte { return bytes.Replace(b, []byte("- one"), []byte("- ONE"), 1) }},
		{name: "вставка вплотную после копии", difference: `"- new"`, firstLine: "3", secondLine: "8", edit: func(b []byte) []byte {
			b = bytes.ReplaceAll(b, []byte("- one"), []byte("- ONE"))
			return bytes.Replace(b, []byte("- ONE\n"), []byte("- ONE\n- new\n"), 1)
		}},
		{name: "пустые строки в конце", difference: "blank line", firstLine: "3", secondLine: "7", edit: func(b []byte) []byte {
			return append(append(bytes.ReplaceAll(b, []byte("- one"), []byte("- ONE")), '\n'), '\n')
		}},
		{name: "строки с шапкой", difference: `"- ONE"`, firstLine: "4", secondLine: "8", header: true, edit: func(b []byte) []byte {
			return replaceSecondTestOccurrence(b, []byte("- one"), []byte("- ONE"))
		}},
		{name: "CRLF без возврата каретки в отличии", difference: `"- ONE"`, firstLine: "3", secondLine: "7", original: []byte("# T\r\n\r\n- one\r\n\r\n- two\r\n\r\n"), edit: func(b []byte) []byte {
			return replaceSecondTestOccurrence(b, []byte("- one"), []byte("- ONE"))
		}},
		{name: "пустая строка внутри блока кода", difference: "blank line", firstLine: "3", secondLine: "11", original: []byte("# T\n\n```\na\n\nb\n```\n\n- two\n\n"), edit: func(b []byte) []byte {
			return replaceSecondTestOccurrence(b, []byte("a\n\nb"), []byte("a\nb"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := tc.original
			if original == nil {
				original = []byte("# T\n\n- one\n\n- two\n\n")
			}
			codex, out := setupRepeatedPartAdviceFixture(t, original, tc.header)
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
			for _, want := range []string{"more than once", "line " + tc.firstLine, "line " + tc.secondLine, tc.difference, "make every copy the same"} {
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

func replaceSecondTestOccurrence(data, old, replacement []byte) []byte {
	first := bytes.Index(data, old)
	if first < 0 {
		return data
	}
	second := bytes.Index(data[first+len(old):], old)
	if second < 0 {
		return data
	}
	second += first + len(old)
	result := append([]byte(nil), data[:second]...)
	result = append(result, replacement...)
	return append(result, data[second+len(old):]...)
}

func setupRepeatedPartAdviceFixture(t *testing.T, original []byte, withHeader ...bool) (string, string) {
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
	if len(withHeader) > 0 && withHeader[0] {
		m.header = true
	}
	if err := writeManifest(manifestPath, m, m.links); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	return codex, out
}
