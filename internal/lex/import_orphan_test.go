package lex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImportLeavesNoOwnOrphans(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	source := filepath.Join(root, "docs", "b.md")
	if err := os.MkdirAll(filepath.Dir(source), directoryMode); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, source, []byte("# B\n\nkeep paragraph\n\ndropped paragraph\n"))
	if err := ImportWithOptions(codex, source, ImportOptions{Home: root}); err != nil {
		t.Fatalf("initial import: %v", err)
	}
	manifestPath := filepath.Join(codex, "manifest", codexKey("docs/b.md")+".yaml")
	old, err := readManifest(manifestPath)
	if err != nil {
		t.Fatalf("read initial manifest: %v", err)
	}
	var droppedID string
	for _, id := range old.parts {
		path := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
		body, err := fragmentBody(mustRead(t, path))
		if err != nil {
			t.Fatalf("read fragment %s: %v", id, err)
		}
		if string(body) == "dropped paragraph\n" {
			droppedID = id
		}
	}
	if droppedID == "" {
		t.Fatalf("initial import has no dropped paragraph fragment: %v", old.parts)
	}
	if err := os.Remove(manifestPath); err != nil {
		t.Fatalf("remove initial manifest: %v", err)
	}
	mustWrite(t, source, []byte("# B\n\nkeep paragraph\n"))
	if err := ImportWithOptions(codex, source, ImportOptions{Home: root}); err != nil {
		t.Fatalf("primary import: %v", err)
	}
	if _, err := os.Stat(filepath.Join(codex, "frag", filepath.FromSlash(droppedID)+".md")); !os.IsNotExist(err) {
		t.Fatalf("dropped fragment %s remains after primary import (stat error %v)", droppedID, err)
	}
	if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
		t.Fatalf("check after primary import: %v", err)
	}
}
