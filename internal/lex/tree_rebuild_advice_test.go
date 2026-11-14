package lex

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// treeRebuildNote is the part of tree advice that says lex build rewrites the
// manifest output, not the worktree copy that was checked.
func treeRebuildNote(mainOutput, treeOutput string) string {
	return "`lex build` rewrites " + strconv.Quote(mainOutput) + ", not " + strconv.Quote(treeOutput)
}

// Advice to rebuild a worktree copy that carries a foreign header names what
// lex build rewrites, and following it leaves check clean.
func TestCheckTreeRebuildAdviceNamesManifestOutput(t *testing.T) {
	fx := newTreeOutputFixture(t, false)
	mustWrite(t, fx.treeOutput, append(generatedMarkerForCodex("x", filepath.Join(filepath.Dir(fx.codex), "other")), fx.built...))
	_, err := CheckWithWarnings(fx.codex, fx.opts)
	if err == nil || !strings.Contains(err.Error(), "the file carries") || !strings.Contains(err.Error(), treeRebuildNote(fx.mainOutput, fx.treeOutput)) {
		t.Fatalf("check = %v, want the foreign header advice to say %s", err, treeRebuildNote(fx.mainOutput, fx.treeOutput))
	}
	if err := Build(fx.codex, false); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := os.ReadFile(fx.mainOutput)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, fx.treeOutput, rebuilt)
	if _, err := CheckWithWarnings(fx.codex, fx.opts); err != nil {
		t.Fatalf("check after the advice = %v", err)
	}
}

// The same note for a staged worktree copy that carries a foreign header.
func TestStagedWorktreeForeignHeaderNamesManifestOutput(t *testing.T) {
	_, worktree, codex, output, git := stagedWorktreeRepo(t)
	wTO := filepath.Join(worktree, filepath.Base(output))
	data, err := os.ReadFile(wTO)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, wTO, append(generatedMarkerForCodex("x", filepath.Join(filepath.Dir(codex), "other")), data...))
	if err := git(worktree, "add", filepath.Base(output)); err != nil {
		t.Fatal(err)
	}
	err = checkIn(t, worktree, codex)
	if err == nil || !strings.Contains(err.Error(), "the file carries") || !strings.Contains(err.Error(), treeRebuildNote(output, wTO)) {
		t.Fatalf("staged check = %v, want the foreign header advice to say %s", err, treeRebuildNote(output, wTO))
	}
}
