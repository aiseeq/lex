package lex

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportOrphanCleanupReadError(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	source := filepath.Join(root, "docs", "b.md")
	if err := os.MkdirAll(filepath.Dir(source), directoryMode); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, source, []byte("# b\n\nx\n\nzzz\n"))
	if err := ImportWithOptions(codex, source, ImportOptions{Home: root}); err != nil {
		t.Fatalf("initial import: %v", err)
	}
	manifestPath := filepath.Join(codex, "manifest", codexKey("docs/b.md")+".yaml")
	m, err := readManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var orphanPath string
	for _, id := range m.parts {
		path := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("zzz")) {
			orphanPath = path
			break
		}
	}
	if orphanPath == "" {
		t.Fatal("initial import has no fragment containing zzz")
	}
	if err := os.Remove(manifestPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(orphanPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "missing-fragment"), orphanPath); err != nil {
		t.Fatal(err)
	}

	before := snapshotTree(t, codex)
	mustWrite(t, source, []byte("# b\n\nx\n"))
	err = ImportWithOptions(codex, source, ImportOptions{Home: root})
	if err == nil || !strings.Contains(err.Error(), "read topic fragment") {
		t.Fatalf("import error = %v, want topic fragment read error", err)
	}
	if _, err := os.Lstat(manifestPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed import wrote manifest: %v", err)
	}
	if after := snapshotTree(t, codex); !equalSnapshots(before, after) {
		t.Fatalf("failed import changed codex state\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			snapshot[rel] = "symlink:" + target
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snapshot[rel] = "file:" + string(contents)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func equalSnapshots(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for path, content := range a {
		if b[path] != content {
			return false
		}
	}
	return true
}
