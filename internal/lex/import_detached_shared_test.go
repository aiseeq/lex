package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestImportKeepsOccupiedSharedFragment(t *testing.T) {
	t.Run("id-sameslug", testImportKeepsChangedSharedFragment)
	t.Run("id-orphan", testImportReusesOwnOrphanFragment)
	t.Run("id-goal", testImportKeepsOccupiedFragmentHeader)
}

func testImportKeepsChangedSharedFragment(t *testing.T) {
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

func testImportReusesOwnOrphanFragment(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	a := filepath.Join(root, "docs", "a.md")
	if err := os.MkdirAll(filepath.Dir(a), directoryMode); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, a, []byte("# a\n\nx\n"))
	if err := ImportWithOptions(codex, a, ImportOptions{Home: root}); err != nil {
		t.Fatalf("initial import: %v", err)
	}
	manifestPath := filepath.Join(codex, "manifest", codexKey("docs/a.md")+".yaml")
	if err := os.Remove(manifestPath); err != nil {
		t.Fatalf("remove a.md manifest: %v", err)
	}

	want := []byte("# a\n\nX!\n")
	mustWrite(t, a, want)
	if err := ImportWithOptions(codex, a, ImportOptions{Home: root}); err != nil {
		t.Fatalf("primary import: %v", err)
	}
	m, err := readManifest(manifestPath)
	if err != nil {
		t.Fatalf("read imported manifest: %v", err)
	}
	if !slices.Contains(m.parts, codexKey("docs/a.md")+"/x-1") {
		t.Fatalf("own orphan ID was not reused; parts = %v", m.parts)
	}
	assembled, err := manifestBody(codex, m)
	if err != nil {
		t.Fatalf("assemble imported fragments: %v", err)
	}
	if !bytes.Equal(assembled, want) {
		t.Fatalf("imported fragments differ from source: got %q, want %q; parts %v", assembled, want, m.parts)
	}
	if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
		t.Fatalf("check after primary import: %v", err)
	}
	refreshWant := []byte("# a\n\nX!\n\nmore\n")
	mustWrite(t, a, refreshWant)
	if err := ImportWithOptions(codex, a, ImportOptions{Home: root, Refresh: true}); err != nil {
		t.Fatalf("refresh after primary import: %v", err)
	}
	if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
		t.Fatalf("check after refresh: %v", err)
	}
}

func testImportKeepsOccupiedFragmentHeader(t *testing.T) {
	root, codex, a, b, c, sharedID := detachedSharedFixture(t)
	sharedPath := filepath.Join(codex, "frag", filepath.FromSlash(sharedID)+".md")
	sharedBefore := mustRead(t, sharedPath)
	withGoal := bytes.Replace(sharedBefore, []byte("цель: \n"), []byte("цель: preserve goal\n"), 1)
	if bytes.Equal(withGoal, sharedBefore) {
		t.Fatal("shared fragment has no empty goal field")
	}
	mustWrite(t, sharedPath, withGoal)
	for _, name := range []string{"a", "b", "c"} {
		manifestPath := filepath.Join(codex, "manifest", codexKey("docs/"+name+".md")+".yaml")
		m, err := readManifest(manifestPath)
		if err != nil {
			t.Fatalf("read %s manifest: %v", name, err)
		}
		if slices.Contains(m.parts, sharedID) {
			m.fragmentHashes[sharedID] = sha(withGoal)
			if err := writeManifest(manifestPath, m, m.links); err != nil {
				t.Fatalf("write %s manifest: %v", name, err)
			}
		}
	}
	sharedBefore = append([]byte(nil), withGoal...)
	bBefore, cBefore := mustRead(t, b), mustRead(t, c)
	manifestPath := filepath.Join(codex, "manifest", codexKey("docs/a.md")+".yaml")
	if err := os.Remove(manifestPath); err != nil {
		t.Fatalf("remove a.md manifest: %v", err)
	}
	want := mustRead(t, a)
	if err := ImportWithOptions(codex, a, ImportOptions{Home: root}); err != nil {
		t.Fatalf("primary import: %v", err)
	}
	if got := mustRead(t, sharedPath); !bytes.Equal(got, sharedBefore) {
		t.Fatal("primary import changed the occupied shared fragment header or body")
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
