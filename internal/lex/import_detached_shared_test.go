package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestImportKeepsOccupiedSharedFragment(t *testing.T) {
	root, codex, a, b, c, sharedID := detachedSharedFixture(t)
	sharedPath := filepath.Join(codex, "frag", filepath.FromSlash(sharedID)+".md")
	sharedBefore := mustRead(t, sharedPath)
	bBefore, cBefore := mustRead(t, b), mustRead(t, c)
	manifestPath := filepath.Join(codex, "manifest", codexKey("docs/a.md")+".yaml")
	if err := os.Remove(manifestPath); err != nil {
		t.Fatalf("remove a.md manifest: %v", err)
	}

	want := []byte("# a\n\nShared, para!\n")
	mustWrite(t, a, want)
	if err := ImportWithOptions(codex, a, ImportOptions{Home: root}); err != nil {
		t.Fatalf("primary import: %v", err)
	}
	if got := mustRead(t, sharedPath); !bytes.Equal(got, sharedBefore) {
		t.Fatal("primary import changed the occupied shared fragment")
	}
	if err := Build(codex, false); err != nil {
		t.Fatalf("build: %v", err)
	}
	if got := mustRead(t, a); !bytes.Equal(got, want) {
		t.Fatalf("build changed a.md: got %q, want %q", got, want)
	}
	if got := mustRead(t, b); !bytes.Equal(got, bBefore) {
		t.Fatal("b.md changed during primary import")
	}
	if got := mustRead(t, c); !bytes.Equal(got, cBefore) {
		t.Fatal("c.md changed during primary import")
	}
	if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
		t.Fatalf("check after primary import: %v", err)
	}
}
