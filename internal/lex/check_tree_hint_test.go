package lex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// treeOutputFixture is a codex that built docs/guide.md under a main checkout,
// with check options that read the output's copy under a worktree.
type treeOutputFixture struct {
	codex, mainOutput, treeOutput string
	opts                          CheckOptions
	built                         []byte
}

// newTreeOutputFixture builds "- one\n- two\n" into the main checkout's docs/guide.md and creates the worktree's docs directory without the copy.
func newTreeOutputFixture(t *testing.T, codexInMain bool) treeOutputFixture {
	t.Helper()
	root := t.TempDir()
	main, tree := filepath.Join(root, "main"), filepath.Join(root, "tree")
	fx := treeOutputFixture{
		codex:      filepath.Join(root, "codex"),
		mainOutput: filepath.Join(main, "docs", "guide.md"),
		treeOutput: filepath.Join(tree, "docs", "guide.md"),
		opts:       CheckOptions{OutputsUnder: main, Tree: tree},
	}
	if codexInMain {
		fx.codex = filepath.Join(main, "codex")
	}
	if err := importData(fx.codex, fx.mainOutput, "guide.md", []byte("- one\n- two\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(fx.codex, false); err != nil {
		t.Fatal(err)
	}
	built, err := os.ReadFile(fx.mainOutput)
	if err != nil {
		t.Fatal(err)
	}
	fx.built = built
	if err := os.MkdirAll(filepath.Dir(fx.treeOutput), directoryMode); err != nil {
		t.Fatal(err)
	}
	return fx
}

func TestCheckTreeAdviceNamesManifestOutput(t *testing.T) {
	for _, tc := range []struct {
		name         string
		editFragment bool
		codexInMain  bool
	}{
		{name: "worktree file only"},
		{name: "worktree file and fragment", editFragment: true},
		{name: "codex in main tree", codexInMain: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newTreeOutputFixture(t, tc.codexInMain)
			codex, treeOutput := fx.codex, fx.treeOutput
			if err := os.WriteFile(treeOutput, []byte(strings.Replace(string(fx.built), "- two", "- TWO", 1)), 0o644); err != nil {
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

			_, err := CheckWithWarnings(codex, fx.opts)
			if err == nil {
				t.Fatal("expected a tree output mismatch")
			}
			advice := err.Error()
			if !strings.Contains(advice, treeOutput) {
				t.Errorf("advice does not name worktree copy %q: %v", treeOutput, err)
			}
			if note := "which changes the fragments in the main checkout"; strings.Contains(advice, note) != tc.codexInMain {
				t.Errorf("advice names main checkout fragments = %t, want %t: %v", strings.Contains(advice, note), tc.codexInMain, err)
			}
			for command := range strings.SplitSeq(advice, "`") {
				if strings.HasPrefix(command, "lex ") && strings.Contains(command, treeOutput) {
					t.Errorf("lex command contains worktree path %q: %s", treeOutput, command)
				}
			}
		})
	}
}
