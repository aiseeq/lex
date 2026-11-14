package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefreshKeepsSharedFragmentAbsorbCannotTake(t *testing.T) {
	for _, tc := range []struct {
		name, content string
	}{
		{"whole", "x\n"},
		{"comment", "# a\n<!-- c -->\nown line\n"},
		{"fence", "# a\n```\n"},
		{"same id different body", "shared para!\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, codex, a, b := sharedEditFixture(t)
			c := filepath.Join(root, "docs", "c.md")
			mustWrite(t, c, []byte("# C\n\nshared para\n"))
			if err := ImportWithOptions(codex, c, ImportOptions{Home: root}); err != nil {
				t.Fatal(err)
			}
			maPath := filepath.Join(codex, "manifest", codexKey("docs/a.md")+".yaml")
			mbPath := filepath.Join(codex, "manifest", codexKey("docs/b.md")+".yaml")
			mcPath := filepath.Join(codex, "manifest", codexKey("docs/c.md")+".yaml")
			ma, err := readManifest(maPath)
			if err != nil {
				t.Fatal(err)
			}
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
			mc, err := readManifest(mcPath)
			if err != nil {
				t.Fatal(err)
			}
			for i, id := range mc.parts {
				raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
				if err != nil {
					t.Fatal(err)
				}
				body, err := fragmentBody(raw)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Equal(body, []byte("shared para\n")) {
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
			beforeB, _ := os.ReadFile(b)
			beforeC, _ := os.ReadFile(c)
			mbBefore, _ := os.ReadFile(mbPath)
			mcBefore, _ := os.ReadFile(mcPath)
			mustWrite(t, a, []byte(tc.content))
			entries, err := StatusWithHome(codex, root)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, entry := range entries {
				if entry.Output == a {
					found = true
					if !strings.HasPrefix(entry.Command, "lex import --refresh ") {
						t.Fatalf("status command = %q", entry.Command)
					}
				}
			}
			if !found {
				t.Fatal("status has no entry")
			}
			_, checkAdvice := CheckWithWarnings(codex, CheckOptions{})
			if checkAdvice == nil || !strings.Contains(checkAdvice.Error(), "lex import --refresh") {
				t.Fatalf("check advice = %v", checkAdvice)
			}
			if err := ImportWithOptions(codex, a, ImportOptions{Refresh: true, Home: root}); err != nil {
				t.Fatalf("refresh: %v", err)
			}
			if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
				t.Fatalf("check: %v", err)
			}
			if got, _ := os.ReadFile(b); !bytes.Equal(got, beforeB) {
				t.Fatal("b changed")
			}
			if got, _ := os.ReadFile(c); !bytes.Equal(got, beforeC) {
				t.Fatal("c changed")
			}
			if got, _ := os.ReadFile(mbPath); !bytes.Equal(got, mbBefore) {
				t.Fatal("b manifest changed")
			}
			if got, _ := os.ReadFile(mcPath); !bytes.Equal(got, mcBefore) {
				t.Fatal("c manifest changed")
			}
			refreshed, err := readManifest(maPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range refreshed.parts {
				if id == shared {
					t.Fatal("a still lists shared fragment")
				}
			}
			if _, err := os.Stat(filepath.Join(codex, "frag", filepath.FromSlash(shared)+".md")); err != nil {
				t.Fatalf("shared fragment missing: %v", err)
			}
			if got, _ := os.ReadFile(a); !bytes.Equal(got, []byte(tc.content)) {
				t.Fatalf("a changed: %q", got)
			}
		})
	}
}
