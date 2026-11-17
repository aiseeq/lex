package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAbsorbKeepsDetachedSharedFragment(t *testing.T) {
	for _, tc := range []struct {
		name, content string
	}{
		{name: "d-same", content: "# a\n\nshared para\n\nnew one\n"},
		{name: "d-same-only", content: "# a\n\nshared para\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, codex, a, b, c, sharedID := detachedSharedFixture(t)
			sharedPath := filepath.Join(codex, "frag", filepath.FromSlash(sharedID)+".md")
			sharedBefore := mustRead(t, sharedPath)
			bBefore := mustRead(t, b)
			cBefore := mustRead(t, c)

			mustWrite(t, a, []byte("# a\n"))
			if err := Absorb(codex, a, AbsorbOptions{}); err != nil {
				t.Fatalf("detach shared fragment from a: %v", err)
			}
			if _, err := os.Stat(sharedPath); err != nil {
				t.Fatalf("detached shared fragment disappeared: %v", err)
			}

			mustWrite(t, a, []byte(tc.content))
			if err := Absorb(codex, a, AbsorbOptions{}); err != nil {
				t.Fatalf("absorb restored shared text: %v", err)
			}
			if got := mustRead(t, sharedPath); !bytes.Equal(got, sharedBefore) {
				t.Fatalf("detached shared fragment changed:\n got %q\nwant %q", got, sharedBefore)
			}
			if got := mustRead(t, b); !bytes.Equal(got, bBefore) {
				t.Fatalf("b.md changed during absorb")
			}
			if got := mustRead(t, c); !bytes.Equal(got, cBefore) {
				t.Fatalf("c.md changed during absorb")
			}
			if err := Build(codex, false); err != nil {
				t.Fatalf("build: %v", err)
			}
			if got := mustRead(t, a); !bytes.Equal(got, []byte(tc.content)) {
				t.Fatalf("build changed a.md: got %q, want %q", got, tc.content)
			}
			if got := mustRead(t, b); !bytes.Equal(got, bBefore) {
				t.Fatalf("build changed b.md")
			}
			if got := mustRead(t, c); !bytes.Equal(got, cBefore) {
				t.Fatalf("build changed c.md")
			}
			if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
				t.Fatalf("check after absorb: %v", err)
			}
		})
	}
}

func detachedSharedFixture(t *testing.T) (root, codex, a, b, c, sharedID string) {
	t.Helper()
	root = t.TempDir()
	codex = filepath.Join(root, "codex")
	a, b, c = filepath.Join(root, "docs", "a.md"), filepath.Join(root, "docs", "b.md"), filepath.Join(root, "docs", "c.md")
	if err := os.MkdirAll(filepath.Dir(a), directoryMode); err != nil {
		t.Fatalf("create docs directory: %v", err)
	}
	for _, output := range []struct{ path, title string }{{a, "a"}, {b, "b"}, {c, "c"}} {
		mustWrite(t, output.path, []byte("# "+output.title+"\n\nshared para\n"))
		if err := ImportWithOptions(codex, output.path, ImportOptions{Home: root}); err != nil {
			t.Fatalf("import %s: %v", output.path, err)
		}
	}
	manifests := make([]manifest, 3)
	paths := []string{
		filepath.Join(codex, "manifest", codexKey("docs/a.md")+".yaml"),
		filepath.Join(codex, "manifest", codexKey("docs/b.md")+".yaml"),
		filepath.Join(codex, "manifest", codexKey("docs/c.md")+".yaml"),
	}
	for i, path := range paths {
		m, err := readManifest(path)
		if err != nil {
			t.Fatalf("read manifest %s: %v", path, err)
		}
		manifests[i] = m
	}
	for _, id := range manifests[0].parts {
		body := readFragmentBody(t, codex, id)
		if bytes.Equal(body, []byte("shared para\n")) {
			sharedID = id
			break
		}
	}
	if sharedID == "" {
		t.Fatal("shared paragraph fragment not found")
	}
	for i := 1; i < len(manifests); i++ {
		m := manifests[i]
		for partIndex, id := range m.parts {
			if !bytes.Equal(readFragmentBody(t, codex, id), []byte("shared para\n")) {
				continue
			}
			delete(m.fragmentHashes, id)
			m.parts[partIndex] = sharedID
			m.fragmentHashes[sharedID] = manifests[0].fragmentHashes[sharedID]
			if err := os.Remove(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")); err != nil {
				t.Fatalf("remove duplicate fragment %s: %v", id, err)
			}
			break
		}
		if err := writeManifest(paths[i], m, m.links); err != nil {
			t.Fatalf("write shared manifest %s: %v", paths[i], err)
		}
	}
	if err := Build(codex, false); err != nil {
		t.Fatalf("build shared fixture: %v", err)
	}
	if !strings.Contains(sharedID, "shared-para") {
		t.Fatalf("unexpected shared ID %q", sharedID)
	}
	return root, codex, a, b, c, sharedID
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
