package lex

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestStagedWorktreeAdviceNamesMainCheckout(t *testing.T) {
	main, worktree, codex, git := codexInsideProjectWorktree(t)
	output := filepath.Join(main, "CLAUDE.md")
	worktreeOutput := filepath.Join(worktree, "CLAUDE.md")
	data, err := os.ReadFile(worktreeOutput)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(worktreeOutput, []byte(strings.Replace(string(data), "Original instruction.", "Manual instruction.", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	fragDir := filepath.Join(worktree, "codex", "frag")
	var fragment string
	if err = filepath.WalkDir(fragDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".md") {
			fragment = path
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if fragment == "" {
		t.Fatal("no fragment found")
	}
	fragData, err := os.ReadFile(fragment)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(fragment, append(fragData, []byte("\nFragment update.\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = git(worktree, "add", "CLAUDE.md", filepath.ToSlash(strings.TrimPrefix(fragment, worktree+string(filepath.Separator)))); err != nil {
		t.Fatal(err)
	}
	err = checkIn(t, worktree, codex)
	if err == nil {
		t.Fatal("expected staged output mismatch")
	}
	message := err.Error()
	for _, path := range []string{strconv.Quote(output), strconv.Quote(worktreeOutput)} {
		if !strings.Contains(message, path) {
			t.Errorf("advice does not name %s: %v", path, err)
		}
	}
	if !strings.Contains(message, "carry the edit into "+strconv.Quote(output)+" before running `lex absorb`") {
		t.Errorf("advice does not explain moving the edit to the main checkout: %v", err)
	}
	if !strings.Contains(message, "which changes the fragments in the main checkout") {
		t.Errorf("advice for a codex inside the project does not say absorb changes the main checkout's fragments: %v", err)
	}
	if !strings.Contains(message, "`lex build` rewrites "+strconv.Quote(output)+", not "+strconv.Quote(worktreeOutput)) {
		t.Errorf("build warning does not name the file it rewrites: %v", err)
	}
}

func TestStagedWorktreeInvalidManifestNamesTreeCopy(t *testing.T) {
	main, worktree, codex, git := codexInsideProjectWorktree(t)
	manifest, err := filepath.Glob(filepath.Join(main, "codex", "manifest", "*.yaml"))
	if err != nil || len(manifest) == 0 {
		t.Fatalf("find manifest: %v, %v", manifest, err)
	}
	mainManifest := manifest[0]
	rel, err := filepath.Rel(main, mainManifest)
	if err != nil {
		t.Fatal(err)
	}
	worktreeManifest := filepath.Join(worktree, rel)
	raw, err := os.ReadFile(worktreeManifest)
	if err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(string(raw), "parts:\n", "links:\n  malformed\nparts:\n", 1)
	if err = os.WriteFile(worktreeManifest, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = git(worktree, "add", filepath.ToSlash(rel)); err != nil {
		t.Fatal(err)
	}
	err = checkIn(t, worktree, codex)
	if err == nil {
		t.Fatal("expected invalid manifest")
	}
	line := ""
	for candidate := range strings.SplitSeq(err.Error(), "\n") {
		if strings.Contains(candidate, "invalid manifest") {
			line = candidate
			break
		}
	}
	if line == "" {
		t.Fatalf("invalid manifest line missing from error: %v", err)
	}
	if !strings.Contains(line, worktreeManifest) || strings.Contains(line, mainManifest) {
		t.Fatalf("invalid manifest must name the worktree copy %q, got %s", worktreeManifest, line)
	}
	if !strings.Contains(line, "invalid links entry") {
		t.Fatalf("expected invalid links entry, got %s", line)
	}
}

func TestStagedWorktreeRefsOnlyNamesTreePaths(t *testing.T) {
	main, worktree, codex, git := codexInsideProjectWorktree(t)
	output := filepath.Join(worktree, "CLAUDE.md")
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(output, append(data, []byte("\n[x](missing.md)\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = git(worktree, "add", "CLAUDE.md"); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(worktree); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	warnings, err := CheckWithWarnings(codex, CheckOptions{Staged: true, RefsOnly: true})
	if err != nil {
		t.Fatalf("refs-only staged check failed: %v", err)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, output) || strings.Contains(joined, filepath.Join(main, "CLAUDE.md")) {
		t.Fatalf("finding should name worktree output %q: %v", output, warnings)
	}
	if !strings.Contains(joined, "kind 3") || !strings.Contains(joined, "missing.md") {
		t.Fatalf("expected missing-link warning, got %v", warnings)
	}
}
