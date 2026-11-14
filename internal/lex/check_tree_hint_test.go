package lex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckTreeAdviceNamesManifestOutput(t *testing.T) {
	for _, tc := range []struct {
		name         string
		editFragment bool
	}{
		{name: "worktree file only"},
		{name: "worktree file and fragment", editFragment: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			codex := filepath.Join(root, "codex")
			main := filepath.Join(root, "main")
			tree := filepath.Join(root, "tree")
			mainOutput := filepath.Join(main, "docs", "guide.md")
			treeOutput := filepath.Join(tree, "docs", "guide.md")
			if err := importData(codex, mainOutput, "guide.md", []byte("- one\n- two\n"), false); err != nil {
				t.Fatal(err)
			}
			if err := Build(codex, false); err != nil {
				t.Fatal(err)
			}
			built, err := os.ReadFile(mainOutput)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(treeOutput), directoryMode); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(treeOutput, []byte(strings.Replace(string(built), "- two", "- TWO", 1)), 0o644); err != nil {
				t.Fatal(err)
			}
			if tc.editFragment {
				fragment := fragmentOf(t, codex, "guide.md")
				data, err := os.ReadFile(fragment)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(fragment, []byte(strings.Replace(string(data), "- one", "- ONE", 1)), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			_, err = CheckWithWarnings(codex, CheckOptions{OutputsUnder: main, Tree: tree})
			if err == nil {
				t.Fatal("expected a tree output mismatch")
			}
			advice := err.Error()
			if !strings.Contains(advice, treeOutput) {
				t.Errorf("advice does not name worktree copy %q: %v", treeOutput, err)
			}
			for command := range strings.SplitSeq(advice, "`") {
				if strings.HasPrefix(command, "lex ") && strings.Contains(command, treeOutput) {
					t.Errorf("lex command contains worktree path %q: %s", treeOutput, command)
				}
			}
		})
	}
}
