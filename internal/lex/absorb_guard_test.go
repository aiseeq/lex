package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAbsorbRefusesWhenRebuildDiffers(t *testing.T) {
	codex, out, manifestPath, m := purposeFixture(t, "alpha\n\nbeta\n")
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	output := []byte("changed\n\nnew item\n\nbeta\n")
	mustWrite(t, out, output)
	fragmentPath := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md")
	beforeFragment, err := os.ReadFile(fragmentPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeTree := treeBytes(t, filepath.Join(codex, "frag"))
	original := absorbReplaceFragmentBody
	absorbReplaceFragmentBody = func(file, old, repl []byte) []byte {
		return original(file, old, append(append([]byte(nil), repl...), []byte("corrupted\n")...))
	}
	defer func() { absorbReplaceFragmentBody = original }()
	err = Absorb(codex, out, AbsorbOptions{DraftDir: codex})
	if err == nil || !strings.Contains(err.Error(), "absorbed fragments would not rebuild this file") ||
		!strings.Contains(err.Error(), "first differing line") || !strings.Contains(err.Error(), "draft saved to ") {
		t.Fatalf("absorb error = %v; want rebuild mismatch and saved draft", err)
	}
	afterFragment, err := os.ReadFile(fragmentPath)
	if err != nil {
		t.Fatal(err)
	}
	afterManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeFragment, afterFragment) || !bytes.Equal(beforeManifest, afterManifest) ||
		!bytes.Equal(beforeTree, treeBytes(t, filepath.Join(codex, "frag"))) {
		t.Fatal("refused absorb changed fragment or manifest")
	}
	drafts, err := filepath.Glob(filepath.Join(codex, "drafts", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 {
		t.Fatalf("draft files = %v; want exactly one", drafts)
	}
}
