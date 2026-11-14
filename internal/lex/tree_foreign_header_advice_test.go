package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// foreignMarker is a lex header of another codex next to codex.
func foreignMarker(codex string) []byte {
	return generatedMarkerForCodex("x", filepath.Join(filepath.Dir(codex), "other"))
}

// A worktree copy that carries a foreign header is fixed in its own header
// line: the advice never rebuilds the manifest output, whose own edits
// build --force would erase, and following it leaves check clean.
func TestCheckTreeForeignHeaderAdviceFixesCopy(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mainEdit bool
		header   bool
		want     string
	}{
		{name: "copy of the build", want: "delete line 1, the foreign header, from this worktree copy"},
		{name: "manifest output has its own edit", mainEdit: true, want: "delete line 1, the foreign header, from this worktree copy"},
		{name: "foreign header in place of the codex header", header: true, want: "replace line 1, the foreign header, in this worktree copy with \"<!-- lex: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newTreeOutputFixture(t, false)
			if tc.header {
				manifestPath := manifestOf(t, fx.codex, "guide.md")
				raw, err := os.ReadFile(manifestPath)
				if err != nil {
					t.Fatal(err)
				}
				mustWrite(t, manifestPath, bytes.Replace(raw, []byte("header: false\n"), []byte("header: true\n"), 1))
				if err := BuildWithOptions(fx.codex, BuildOptions{Force: true}); err != nil {
					t.Fatal(err)
				}
				built, err := os.ReadFile(fx.mainOutput)
				if err != nil {
					t.Fatal(err)
				}
				fx.built = built
			}
			if tc.mainEdit {
				mustWrite(t, fx.mainOutput, []byte(strings.Replace(string(fx.built), "- one", "- ONE (main edit)", 1)))
			}
			copied := append(foreignMarker(fx.codex), fx.built...)
			if tc.header {
				copied = append(foreignMarker(fx.codex), fx.built[bytes.IndexByte(fx.built, '\n')+1:]...)
			}
			mustWrite(t, fx.treeOutput, copied)
			mainBefore, err := os.ReadFile(fx.mainOutput)
			if err != nil {
				t.Fatal(err)
			}
			_, err = CheckWithWarnings(fx.codex, fx.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "lex build --force") || strings.Contains(err.Error(), "other edits") {
				t.Fatalf("check = %v, want advice %q without lex build --force", err, tc.want)
			}
			mustWrite(t, fx.treeOutput, fx.built)
			if _, err := CheckWithWarnings(fx.codex, fx.opts); err != nil {
				t.Fatalf("check after the advice = %v", err)
			}
			mainAfter, err := os.ReadFile(fx.mainOutput)
			if err != nil || !bytes.Equal(mainAfter, mainBefore) {
				t.Fatalf("check changed the manifest output: %q, %v", mainAfter, err)
			}
		})
	}
}

// A copy that differs beyond its foreign header is told to run the check
// again after the header line is fixed.
func TestCheckTreeForeignHeaderAdviceNamesOtherEdits(t *testing.T) {
	fx := newTreeOutputFixture(t, false)
	mustWrite(t, fx.treeOutput, append(foreignMarker(fx.codex), strings.Replace(string(fx.built), "- two", "- TWO", 1)...))
	_, err := CheckWithWarnings(fx.codex, fx.opts)
	if err == nil || !strings.Contains(err.Error(), "delete line 1, the foreign header") || !strings.Contains(err.Error(), "run the check again") {
		t.Fatalf("check = %v, want the header fix and a second check for the other edits", err)
	}
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
	if err == nil || !strings.Contains(err.Error(), "delete line 1, the foreign header, from this worktree copy") || !strings.Contains(err.Error(), "then git add the copy") || strings.Contains(err.Error(), "lex build --force") {
		t.Fatalf("staged check = %v, want advice to delete the header line and git add the copy", err)
	}
	mustWrite(t, wTO, data)
	if err := git(worktree, "add", filepath.Base(output)); err != nil {
		t.Fatal(err)
	}
	if err := checkIn(t, worktree, codex); err != nil {
		t.Fatalf("staged check after the advice = %v", err)
	}
}
