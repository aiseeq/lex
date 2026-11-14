package lex

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	if err == nil || !strings.Contains(err.Error(), wTO+" (built by the codex as "+output+")") {
		t.Fatalf("expected staged output error naming worktree file %s, got %v", wTO, err)
	}
	if !strings.Contains(err.Error(), "lex absorb") || !strings.Contains(err.Error(), strconv.Quote(output)) || strings.Contains(err.Error(), "lex absorb --force --codex "+strconv.Quote(codex)+" "+strconv.Quote(wTO)) {
		t.Fatalf("absorb hint must name manifest output %s, got %v", output, err)
	}
}

// codexInsideProjectWorktree commits a project with its codex inside and returns the main checkout, a new worktree, the codex and a git runner.
func codexInsideProjectWorktree(t *testing.T) (string, string, string, func(string, ...string) error) {
	t.Helper()
	base := t.TempDir()
	main, worktree := filepath.Join(base, "main"), filepath.Join(base, "worktree")
	codex := filepath.Join(main, "codex")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(main, "CLAUDE.md")
	if err := importData(codex, output, "CLAUDE.md", []byte("# Guide\n\nOriginal instruction.\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	git := func(dir string, args ...string) error {
		all := append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid"}, args...)
		cmd := exec.Command("git", all...)
		cmd.Dir = dir
		if data, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %s: %w", args, data, err)
		}
		return nil
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"commit", "-q", "-m", "initial"}} {
		if err := git(main, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := git(main, "worktree", "add", "-q", "-b", "test-worktree", worktree); err != nil {
		t.Fatal(err)
	}
	return main, worktree, codex, git
}

func TestStagedWorktreeCodexInsideProjectReportsOnce(t *testing.T) {
	main, worktree, codex, git := codexInsideProjectWorktree(t)
	output := filepath.Join(main, "CLAUDE.md")
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
	if err == nil {
		t.Fatal("expected output mismatch")
	}
	seen := map[string]bool{}
	for line := range strings.SplitSeq(err.Error(), "\n") {
		if seen[line] {
			t.Fatalf("duplicate problem line %q in %v", line, err)
		}
		seen[line] = true
	}
}

func TestStagedWorktreeLinkHintNamesWorktree(t *testing.T) {
	base := t.TempDir()
	main, worktree := filepath.Join(base, "main"), filepath.Join(base, "worktree")
	codex := filepath.Join(base, "codex")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(main, "CLAUDE.md")
	if err := importData(codex, output, "CLAUDE.md", []byte("# Guide\n\nInstruction.\n"), false); err != nil {
		t.Fatal(err)
	}
	manifest, err := filepath.Glob(filepath.Join(codex, "manifest", "CLAUDE.md*.yaml"))
	if err != nil || len(manifest) != 1 {
		t.Fatalf("find manifest: %v %v", manifest, err)
	}
	link := filepath.Join(main, "AGENTS.md")
	if err = os.Symlink("CLAUDE.md", link); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(manifest[0])
	if err != nil {
		t.Fatal(err)
	}
	linkYAML := "links:\n  - path: " + strconv.Quote(link) + "\n    target: \"CLAUDE.md\"\n"
	if !strings.Contains(string(raw), "parts:\n") {
		t.Fatal("manifest has no parts section")
	}
	if err = os.WriteFile(manifest[0], []byte(strings.Replace(string(raw), "parts:\n", linkYAML+"parts:\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = Build(codex, false); err != nil {
		t.Fatal(err)
	}
	git := func(dir string, args ...string) error {
		all := append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid"}, args...)
		cmd := exec.Command("git", all...)
		cmd.Dir = dir
		if data, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %s: %w", args, data, err)
		}
		return nil
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"commit", "-q", "-m", "initial"}} {
		if err = git(main, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = git(main, "worktree", "add", "-q", "-b", "test-worktree", worktree); err != nil {
		t.Fatal(err)
	}
	wLink := filepath.Join(worktree, "AGENTS.md")
	if err = os.Remove(wLink); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(wLink, []byte("regular file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = git(worktree, "add", "AGENTS.md"); err != nil {
		t.Fatal(err)
	}
	err = checkIn(t, worktree, codex)
	if err == nil || !strings.Contains(err.Error(), wLink) || !strings.Contains(err.Error(), "ln -sfn \"CLAUDE.md\" \""+wLink+"\"") {
		t.Fatalf("staged link problem should give worktree path and repair command, got %v", err)
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

func TestStagedWorktreeFragmentHintNamesWorktree(t *testing.T) {
	_, worktree, codex, git := codexInsideProjectWorktree(t)
	wTO := filepath.Join(worktree, "CLAUDE.md")
	if err := os.WriteFile(wTO, []byte("# Guide\n\nManual instruction.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := git(worktree, "add", "CLAUDE.md"); err != nil {
		t.Fatal(err)
	}
	fragments, err := filepath.Glob(filepath.Join(worktree, "codex", "frag", "*", "*.md"))
	if err != nil || len(fragments) == 0 {
		t.Fatalf("find worktree fragments: %v, %v", fragments, err)
	}
	data, err := os.ReadFile(fragments[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(fragments[0], append(data, []byte("Unstaged line.\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	err = checkIn(t, worktree, codex)
	if err == nil {
		t.Fatal("expected output mismatch")
	}
	want := fragments[0] + ": fragment changed on disk but not staged — git add it together with " + wTO
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("hint %q missing from %v", want, err)
	}
}

func TestStagedWorktreeManifestErrorNamesWorktree(t *testing.T) {
	_, worktree, codex, git := codexInsideProjectWorktree(t)
	manifests, err := filepath.Glob(filepath.Join(worktree, "codex", "manifest", "*.yaml"))
	if err != nil || len(manifests) != 1 {
		t.Fatalf("find worktree manifest: %v, %v", manifests, err)
	}
	rel, err := filepath.Rel(worktree, manifests[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = git(worktree, "rm", "-q", "--cached", rel); err != nil {
		t.Fatal(err)
	}
	err = checkIn(t, worktree, codex)
	if err == nil {
		t.Fatal("expected a problem for the manifest removed from the index")
	}
	want := manifests[0] + ": removed from the index in this commit"
	if !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "built by the codex as") {
		t.Fatalf("want %q without the build mark in %v", want, err)
	}
}
