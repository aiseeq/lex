package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefreshKeepsSharedFragmentBytes(t *testing.T) {
	for _, tc := range []struct{ name, from, to string }{
		{"own paragraph", "own para", "own para edited"},
		{"heading", "# A", "# A edited"},
		{"new paragraph", "own para\n", "own para\n\nnew para\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, codex, a, b := sharedEditFixture(t)
			mustWrite(t, a, []byte("# A\n\nshared para\n\nown para\n"))
			if err := Absorb(codex, a, AbsorbOptions{}); err != nil {
				t.Fatal(err)
			}
			m, err := readManifest(filepath.Join(codex, "manifest", codexKey("docs/a.md")+".yaml"))
			if err != nil {
				t.Fatal(err)
			}
			var sharedPath string
			for _, id := range m.parts {
				path := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(raw), "shared para") {
					sharedPath = path
				}
			}
			before, err := os.ReadFile(sharedPath)
			if err != nil {
				t.Fatal(err)
			}
			other, err := os.ReadFile(b)
			if err != nil {
				t.Fatal(err)
			}
			want := []byte(strings.Replace("# A\n\nshared para\n\nown para\n", tc.from, tc.to, 1))
			mustWrite(t, a, want)
			if err := ImportWithOptions(codex, a, ImportOptions{Refresh: true, Home: root}); err != nil {
				t.Fatalf("refresh: %v", err)
			}
			after, err := os.ReadFile(sharedPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("shared fragment changed: before %q, after %q, err %v", before, after, err)
			}
			if err := Build(codex, false); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(a)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("built a = %q, want %q: %v", got, want, err)
			}
			got, err = os.ReadFile(b)
			if err != nil || !bytes.Equal(got, other) {
				t.Fatalf("built b = %q, want %q: %v", got, other, err)
			}
			if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
				t.Fatalf("check: %v", err)
			}
		})
	}
}
