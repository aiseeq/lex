package lex

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func stagedWorktreeRepo(t *testing.T) (main, worktree, codex, output string, git func(string, ...string) error) {
	t.Helper()
	base := t.TempDir()
	main = filepath.Join(base, "main")
	worktree = filepath.Join(base, "worktree")
	codex = filepath.Join(base, "codex")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	output = filepath.Join(main, "CLAUDE.md")
	if err := importData(codex, output, "CLAUDE.md", []byte("# Guide\n\nOriginal instruction.\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	git = func(dir string, args ...string) error {
		all := append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid"}, args...)
		cmd := exec.Command("git", all...)
		cmd.Dir = dir
		if data, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %s: %w", args, data, err)
		}
		return nil
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "CLAUDE.md"}, {"commit", "-q", "-m", "initial"}} {
		if err := git(main, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := git(main, "worktree", "add", "-q", "-b", "test-worktree", worktree); err != nil {
		t.Fatal(err)
	}
	return
}

func TestStagedWorktreeChecksOutputOfCurrentTree(t *testing.T) {
	_, worktree, codex, output, git := stagedWorktreeRepo(t)
	wTO := filepath.Join(worktree, filepath.Base(output))
	data, err := os.ReadFile(wTO)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(wTO, []byte(strings.Replace(string(data), "Original instruction.", "Manual instruction.", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = git(worktree, "add", filepath.Base(output)); err != nil {
		t.Fatal(err)
	}
	err = checkIn(t, worktree, codex)
	if err == nil || !strings.Contains(err.Error(), wTO) {
		t.Fatalf("expected staged output error naming worktree file %s, got %v", wTO, err)
	}
}

func TestStagedWorktreeIgnoresMainCheckoutEdit(t *testing.T) {
	main, worktree, codex, output, git := stagedWorktreeRepo(t)
	wTO := filepath.Join(worktree, filepath.Base(output))
	data, err := os.ReadFile(wTO)
	if err != nil {
		t.Fatal(err)
	}
	manual := []byte(strings.Replace(string(data), "Original instruction.", "Committed manual instruction.", 1))
	if err = os.WriteFile(wTO, manual, 0o644); err != nil {
		t.Fatal(err)
	}
	if err = git(worktree, "add", filepath.Base(output)); err != nil {
		t.Fatal(err)
	}
	if err = git(worktree, "commit", "-q", "-m", "manual output edit"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(wTO, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err = git(worktree, "add", filepath.Base(output)); err != nil {
		t.Fatal(err)
	}
	mainManual := []byte(strings.Replace(string(data), "Original instruction.", "Main checkout manual edit.", 1))
	if err = os.WriteFile(output, mainManual, 0o644); err != nil {
		t.Fatal(err)
	}
	if err = git(main, "add", filepath.Base(output)); err != nil {
		t.Fatal(err)
	}
	if err = checkIn(t, worktree, codex); err != nil {
		t.Fatalf("worktree check should use its indexed output, got %v", err)
	}
}

func TestStagedWorktreeSkipsNestedRepositoryOfMainCheckout(t *testing.T) {
	main, worktree, codex, _, git := stagedWorktreeRepo(t)
	nested := filepath.Join(main, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := git(nested, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(nested, "NOTES.md")
	if err := importData(codex, output, "NOTES.md", []byte("# Notes\n\nNested instruction.\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	copyInWorktree := filepath.Join(worktree, "nested", "NOTES.md")
	if err := os.MkdirAll(filepath.Dir(copyInWorktree), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyInWorktree, []byte("# Notes\n\nUnrelated file of the worktree.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := git(worktree, "add", filepath.Join("nested", "NOTES.md")); err != nil {
		t.Fatal(err)
	}
	if err := checkIn(t, worktree, codex); err != nil {
		t.Fatalf("output inside a repository nested in the main checkout is not a file of the worktree index, got %v", err)
	}
}
