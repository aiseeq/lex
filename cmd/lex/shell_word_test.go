package main

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The git add line names plain paths bare, as before, and quotes the rest so a shell reads them back.
func TestShellWordReadsBack(t *testing.T) {
	for _, path := range []string{"/x/frag/.claude-a%2Db/one-1.md", "/x/правила.md"} {
		if got := shellWord(path); got != path {
			t.Errorf("shellWord(%q) = %q, want it bare", path, got)
		}
	}
	for _, path := range []string{"/x/a $HOME b/f.md", "/x/`t`.md", "/x/a'b.md", "/x/~a.md", "/x/*.md", ""} {
		out, err := exec.Command("sh", "-c", `printf '%s' `+shellWord(path)).Output()
		if err != nil {
			t.Fatalf("sh for %q: %v", path, err)
		}
		if string(out) != path {
			t.Errorf("shellWord(%q) = %s reads back as %q", path, shellWord(path), out)
		}
	}
}

// The printed git add line, run by sh, adds exactly the named files, whatever the home and file names hold.
func TestConfigAddCommandRunsInShell(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home $x 'q'")
	t.Setenv("HOME", home)
	codex := filepath.Join(home, ".claude", "codex")
	var paths []string
	for _, name := range []string{"plain.md", "a b.md", "a$HOME.md", "a'b.md", "правила.md"} {
		path := filepath.Join(codex, "frag", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	if out, err := exec.Command("git", "init", "-q", "--bare", filepath.Join(home, ".claude-config.git")).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	command, err := configAddCommand(codex, paths)
	if err != nil || command == "" {
		t.Fatalf("configAddCommand = %q, %v", command, err)
	}
	if out, err := exec.Command("sh", "-c", command).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v: %s", command, err, out)
	}
	out, err := exec.Command("git", "--git-dir="+filepath.Join(home, ".claude-config.git"), "--work-tree="+home, "ls-files", "-z").Output()
	if err != nil {
		t.Fatal(err)
	}
	staged := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if len(staged) != len(paths) {
		t.Fatalf("staged %q, want %d files", staged, len(paths))
	}
	for i, path := range paths {
		if rel, _ := filepath.Rel(home, path); !slices.Contains(staged, rel) {
			t.Errorf("file %d %q not staged by %s", i, rel, command)
		}
	}
}

// A codex outside ~/.claude gets no hint and no warning, even when the config repository cannot be read or HOME is unset.
func TestConfigAddCommandSilentOutsideClaude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	locked := filepath.Join(home, "locked")
	if err := os.MkdirAll(filepath.Join(locked, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(locked, "repo"), filepath.Join(home, ".claude-config.git")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if _, err := os.Stat(filepath.Join(home, ".claude-config.git")); !errors.Is(err, fs.ErrPermission) {
		t.Skipf("cannot make the config repository unreadable here: %v", err)
	}
	for _, setHome := range []string{home, ""} {
		t.Setenv("HOME", setHome)
		command, err := configAddCommand(filepath.Join(home, "codex"), []string{filepath.Join(home, "codex", "f.md")})
		if command != "" || err != nil {
			t.Errorf("HOME=%q: configAddCommand = %q, %v; want nothing", setHome, command, err)
		}
	}
}
