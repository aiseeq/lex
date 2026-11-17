package lex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedDropAdvisesTwoPasses(t *testing.T) {
	for _, tc := range []struct {
		name    string
		refresh bool
	}{
		{name: "absorb"},
		{name: "refresh", refresh: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, codex, a, b := sharedEditFixture(t)
			c := filepath.Join(root, "docs", "c.md")
			mustWrite(t, c, []byte("# C\n\nshared para\n"))
			if err := ImportWithOptions(codex, c, ImportOptions{Home: root}); err != nil {
				t.Fatal(err)
			}
			mcPath := filepath.Join(codex, "manifest", codexKey("docs/c.md")+".yaml")
			mc := mustReadManifest(t, mcPath)
			ma := mustReadManifest(t, filepath.Join(codex, "manifest", codexKey("docs/a.md")+".yaml"))
			shared := ""
			for _, id := range ma.parts {
				raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
				if err != nil {
					t.Fatal(err)
				}
				body, err := fragmentBody(raw)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(body), "shared para") {
					shared = id
				}
			}
			if shared == "" {
				t.Fatal("shared fragment not found")
			}
			for i, id := range mc.parts {
				if strings.Contains(id, "shared-para") {
					delete(mc.fragmentHashes, id)
					mc.parts[i] = shared
					mc.fragmentHashes[shared] = ma.fragmentHashes[shared]
					if err := os.Remove(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")); err != nil {
						t.Fatal(err)
					}
					break
				}
			}
			if err := writeManifest(mcPath, mc, mc.links); err != nil {
				t.Fatal(err)
			}
			if err := Build(codex, false); err != nil {
				t.Fatal(err)
			}

			mustWrite(t, a, []byte("x\n"))
			var refusal error
			if tc.refresh {
				refusal = ImportWithOptions(codex, a, ImportOptions{Refresh: true, Home: root})
			} else {
				refusal = Absorb(codex, a, AbsorbOptions{DraftDir: codex})
			}
			if refusal == nil {
				t.Fatal("expected refusal")
			}
			for _, want := range []string{"first delete only the shared paragraph `" + shared + "`", "still used by", "b.md", "c.md", "run `lex absorb`", "then make the other edit and run `lex absorb` again"} {
				if !strings.Contains(refusal.Error(), want) {
					t.Errorf("refusal %q does not contain %q", refusal, want)
				}
			}

			// Carry out the advised first pass, then the remaining edit.
			mustWrite(t, a, []byte("# A\n"))
			if err := Absorb(codex, a, AbsorbOptions{}); err != nil {
				t.Fatalf("first pass absorb: %v", err)
			}
			mustWrite(t, a, []byte("x\n"))
			if err := Absorb(codex, a, AbsorbOptions{}); err != nil {
				t.Fatalf("second pass absorb: %v", err)
			}
			if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
				t.Fatalf("check after advised path: %v", err)
			}
			if _, err := os.Stat(b); err != nil {
				t.Fatal(err)
			}
		})
	}
}
