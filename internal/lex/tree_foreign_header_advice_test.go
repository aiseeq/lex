package lex

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// foreignMarker is a lex header of another codex next to codex.
func foreignMarker(codex string) []byte {
	return generatedMarkerForCodex("x", filepath.Join(filepath.Dir(codex), "other"))
}

// copyOverNote is the advice for a worktree copy with a foreign header when the manifest output matches the fragments.
func copyOverNote(mainOutput string) string {
	return "copy " + strconv.Quote(mainOutput) + " over this worktree copy"
}

// A worktree copy that carries a foreign header is replaced by copying the
// manifest output when that matches the fragments; the advice never rebuilds
// the manifest output, whose own edits build --force would erase.
func TestCheckTreeForeignHeaderAdviceKeepsManifestOutput(t *testing.T) {
	t.Run("output matches the fragments", func(t *testing.T) {
		fx := newTreeOutputFixture(t, false)
		mustWrite(t, fx.treeOutput, append(foreignMarker(fx.codex), fx.built...))
		_, err := CheckWithWarnings(fx.codex, fx.opts)
		if err == nil || !strings.Contains(err.Error(), "the file carries") || !strings.Contains(err.Error(), copyOverNote(fx.mainOutput)) || strings.Contains(err.Error(), "lex build --force") {
			t.Fatalf("check = %v, want the foreign header advice to %s without lex build --force", err, copyOverNote(fx.mainOutput))
		}
		mustWrite(t, fx.treeOutput, fx.built)
		if _, err := CheckWithWarnings(fx.codex, fx.opts); err != nil {
			t.Fatalf("check after the advice = %v", err)
		}
	})
	t.Run("output has its own edit", func(t *testing.T) {
		fx := newTreeOutputFixture(t, false)
		mainEdit := []byte(strings.Replace(string(fx.built), "- one", "- ONE (main edit)", 1))
		mustWrite(t, fx.mainOutput, mainEdit)
		mustWrite(t, fx.treeOutput, append(foreignMarker(fx.codex), fx.built...))
		_, err := CheckWithWarnings(fx.codex, fx.opts)
		if err == nil || !strings.Contains(err.Error(), "settle "+strconv.Quote(fx.mainOutput)+" first") || strings.Contains(err.Error(), "lex build --force") || strings.Contains(err.Error(), copyOverNote(fx.mainOutput)) {
			t.Fatalf("check = %v, want advice to settle the edited manifest output first, without lex build --force", err)
		}
	})
}

// The same advice for a staged worktree copy also stages the copy again, and
// following it leaves check --staged clean.
func TestStagedWorktreeForeignHeaderAdviceStagesCopy(t *testing.T) {
	_, worktree, codex, output, git := stagedWorktreeRepo(t)
	wTO := filepath.Join(worktree, filepath.Base(output))
	data, err := os.ReadFile(wTO)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, wTO, append(foreignMarker(codex), data...))
	if err := git(worktree, "add", filepath.Base(output)); err != nil {
		t.Fatal(err)
	}
	err = checkIn(t, worktree, codex)
	if err == nil || !strings.Contains(err.Error(), copyOverNote(output)) || !strings.Contains(err.Error(), "then git add the copy") || strings.Contains(err.Error(), "lex build --force") {
		t.Fatalf("staged check = %v, want advice to %s and git add it", err, copyOverNote(output))
	}
	built, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, wTO, built)
	if err := git(worktree, "add", filepath.Base(output)); err != nil {
		t.Fatal(err)
	}
	if err := checkIn(t, worktree, codex); err != nil {
		t.Fatalf("staged check after the advice = %v", err)
	}
}
