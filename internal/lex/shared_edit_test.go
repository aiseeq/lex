package lex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sharedEditFixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	a, b := filepath.Join(root, "docs", "a.md"), filepath.Join(root, "docs", "b.md")
	if err := os.MkdirAll(filepath.Dir(a), directoryMode); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, a, []byte("# A\n\nshared para\n"))
	mustWrite(t, b, []byte("# B\n\nshared para\n"))
	if err := ImportWithOptions(codex, a, ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, b, ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	maPath := filepath.Join(codex, "manifest", codexKey("docs/a.md")+".yaml")
	mbPath := filepath.Join(codex, "manifest", codexKey("docs/b.md")+".yaml")
	ma, err := readManifest(maPath)
	if err != nil {
		t.Fatal(err)
	}
	mb, err := readManifest(mbPath)
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
	if shared == "" {
		t.Fatal("shared paragraph fragment not found")
	}
	for i, id := range mb.parts {
		if strings.Contains(id, "shared-para") {
			delete(mb.fragmentHashes, id)
			mb.parts[i] = shared
			mb.fragmentHashes[shared] = ma.fragmentHashes[shared]
			if err := os.Remove(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if err := writeManifest(mbPath, mb, mb.links); err != nil {
		t.Fatal(err)
	}
	return root, codex, a, b
}

func TestCheckAdviceSharedEdit(t *testing.T) {
	_, codex, a, b := sharedEditFixture(t)
	data, _ := os.ReadFile(a)
	mustWrite(t, a, []byte(strings.Replace(string(data), "shared para", "SHARED para", 1)))
	_, err := CheckWithWarnings(codex, CheckOptions{})
	if err == nil || !strings.Contains(err.Error(), "--shared") || !strings.Contains(err.Error(), b) {
		t.Fatalf("check advice = %v", err)
	}
}

func TestStatusAdviceSharedEdit(t *testing.T) {
	root, codex, a, b := sharedEditFixture(t)
	data, _ := os.ReadFile(a)
	mustWrite(t, a, []byte(strings.Replace(string(data), "shared para", "SHARED para", 1)))
	entries, err := StatusWithHome(codex, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Output == a {
			if !strings.Contains(entry.Command, "--shared") || !strings.Contains(entry.SharedHint, b) {
				t.Fatalf("status advice = %#v", entry)
			}
			return
		}
	}
	t.Fatalf("status has no entry for %s", a)
}

func TestRefreshRefusesSharedEdit(t *testing.T) {
	root, codex, a, b := sharedEditFixture(t)
	data, _ := os.ReadFile(a)
	mustWrite(t, a, []byte(strings.Replace(string(data), "shared para", "SHARED para", 1)))
	err := ImportWithOptions(codex, a, ImportOptions{Refresh: true, Home: root})
	if err == nil || !strings.Contains(err.Error(), b) || !strings.Contains(err.Error(), "lex absorb --shared") {
		t.Fatalf("refresh error = %v", err)
	}
}
