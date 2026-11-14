package lex

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusNamesMissingOnDisk(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit bool
	}{
		{name: "manifest"},
		{name: "fragment"},
		{name: "private fragment"},
		{name: "fragment with edited output", edit: true},
		{name: "intentional manifest and fragment deletion"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, codex, a, _ := sharedEditFixture(t)
			t.Chdir(root)
			initMissingDiskRepo(t, root)
			manifestPath := filepath.Join(codex, "manifest", codexKey("docs/a.md")+".yaml")
			m := mustReadManifest(t, manifestPath)
			originalA, err := os.ReadFile(a)
			if err != nil {
				t.Fatal(err)
			}
			var fragment, want string
			switch tc.name {
			case "manifest":
				fragment = manifestPath
				if err := os.Remove(manifestPath); err != nil {
					t.Fatal(err)
				}
			case "fragment", "private fragment", "fragment with edited output":
				sharedIDs := map[string]bool{}
				if tc.name == "private fragment" {
					other := mustReadManifest(t, filepath.Join(codex, "manifest", codexKey("docs/b.md")+".yaml"))
					for _, id := range other.parts {
						sharedIDs[id] = true
					}
				}
				for _, id := range m.parts {
					if (tc.name == "private fragment" && !sharedIDs[id]) || (tc.name != "private fragment" && strings.Contains(id, "shared-para")) {
						fragment = filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
						want = missingFragmentAdvice(fragment, id, manifestPath)
						break
					}
				}
				if fragment == "" {
					t.Fatal("shared paragraph fragment not found")
				}
				if tc.edit {
					data, err := os.ReadFile(a)
					if err != nil {
						t.Fatal(err)
					}
					mustWrite(t, a, []byte(strings.Replace(string(data), "shared para", "SHARED para", 1)))
				}
				if err := os.Remove(fragment); err != nil {
					t.Fatal(err)
				}
			case "intentional manifest and fragment deletion":
				other := mustReadManifest(t, filepath.Join(codex, "manifest", codexKey("docs/b.md")+".yaml"))
				shared := map[string]bool{}
				for _, id := range other.parts {
					shared[id] = true
				}
				if err := os.Remove(manifestPath); err != nil {
					t.Fatal(err)
				}
				for _, id := range m.parts {
					if shared[id] {
						continue
					}
					if err := os.Remove(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")); err != nil {
						t.Fatal(err)
					}
				}
			}

			entries, err := StatusWithHome(codex, root)
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "intentional manifest and fragment deletion" {
				if len(entries) != 0 {
					t.Fatalf("status after intentional deletion = %#v; want clean", entries)
				}
				return
			}
			found := false
			for _, entry := range entries {
				if tc.name == "manifest" && strings.Contains(entry.Problem, "manifest is missing on disk") && strings.Contains(entry.Problem, manifestPath) {
					found = strings.Contains(entry.Problem, "restore it with `")
				}
				if (strings.HasPrefix(tc.name, "fragment") || tc.name == "private fragment") && entry.Problem == want {
					found = true
				}
			}
			if !found {
				t.Fatalf("status entries = %#v; missing advice for %s", entries, fragment)
			}
			runMissingDiskRestore(t, root, fragment)
			if tc.edit {
				mustWrite(t, a, originalA)
			}
			entries, err = StatusWithHome(codex, root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("status after restoring %s = %#v, %v; want clean", fragment, entries, err)
			}
		})
	}
}

func runMissingDiskRestore(t *testing.T, root, path string) {
	t.Helper()
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatalf("resolve restore path: %v", err)
	}
	cmd := exec.Command("git", "-C", root, "checkout", "--", rel)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout %s: %s: %v", rel, output, err)
	}
}
