package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiseeq/lex/internal/lex"
)

func captureRunWithStderr(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	stderrPath := filepath.Join(t.TempDir(), "stderr")
	f, err := os.Create(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = f
	stdout, runErr := captureRun(t, args...)
	os.Stderr = old
	if closeErr := f.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	stderr, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	return stdout, string(stderr), runErr
}

func warningFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	output := filepath.Join(root, "instructions.md")
	if err := os.WriteFile(output, []byte("# Sample\n\nOriginal rule.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := lex.ImportWithOptions(codex, output, lex.ImportOptions{Home: root}); err != nil {
		t.Fatalf("import fixture: %v", err)
	}
	if err := lex.Build(codex, false); err != nil {
		t.Fatalf("build fixture: %v", err)
	}
	return root, codex, output
}

func unsetGitEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
}

func gitCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func TestBuildForceFileWarnsAboutEditNotInFragments(t *testing.T) {
	_, codex, output := warningFixture(t)
	editTime := time.Date(2026, 11, 12, 14, 7, 0, 0, time.Local)
	if err := os.WriteFile(output, []byte("# Sample\n\nForeign edit.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(output, editTime, editTime); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := captureRunWithStderr(t, "build", "--codex", codex, "--force", output)
	if err != nil {
		t.Fatalf("build --force failed: %v", err)
	}
	wantTime := editTime.In(time.Local).Format("2006-01-02 15:04:05 MST")
	if !strings.Contains(stderr, "warning: "+output+": --force replaced an edit that was not in the fragments") || !strings.Contains(stderr, wantTime) {
		t.Errorf("warning %q lacks expected message or time %q", stderr, wantTime)
	}
	built, readErr := os.ReadFile(output)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if bytes.Contains(built, []byte("Foreign edit.")) || !bytes.Contains(built, []byte("Original rule.")) {
		t.Errorf("force build did not rebuild output: %q", built)
	}
}

func TestAbsorbWarnsAboutEditNotInFragments(t *testing.T) {
	_, codex, output := warningFixture(t)
	if err := os.WriteFile(output, []byte("# Sample\n\nChanged rule.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := captureRunWithStderr(t, "absorb", "--codex", codex, output)
	if err != nil {
		t.Fatalf("absorb failed: %v", err)
	}
	if !strings.Contains(stderr, "warning: "+output+": absorb moved an edit into the fragments") {
		t.Errorf("missing absorb warning: %q", stderr)
	}
	fragments, err := filepath.Glob(filepath.Join(codex, "frag", "*", "*.md"))
	if err != nil || len(fragments) == 0 {
		t.Fatalf("find fragments: %v, %v", fragments, err)
	}
	found := false
	for _, path := range fragments {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		found = found || bytes.Contains(data, []byte("Changed rule."))
	}
	if !found {
		t.Error("absorbed edit was not written to a fragment")
	}
}

func TestForceAndAbsorbWithoutEditDoNotWarn(t *testing.T) {
	_, codex, output := warningFixture(t)
	_, stderr, err := captureRunWithStderr(t, "build", "--codex", codex, "--force", output)
	if err != nil {
		t.Fatalf("build --force failed: %v", err)
	}
	if strings.Contains(stderr, "warning:") {
		t.Errorf("unexpected build warning: %q", stderr)
	}
	_, stderr, err = captureRunWithStderr(t, "absorb", "--codex", codex, output)
	if err != nil {
		t.Fatalf("absorb failed: %v", err)
	}
	if strings.Contains(stderr, "warning:") {
		t.Errorf("unexpected absorb warning: %q", stderr)
	}
}

func TestEditWarningNamesGitState(t *testing.T) {
	unsetGitEnvironment(t)
	root, codex, output := warningFixture(t)
	gitCommand(t, root, "init", "-q")
	config := []string{"-c", "user.name=Fixture User", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false"}
	gitCommand(t, root, append(append([]string{}, config...), "add", "instructions.md", "codex")...)
	gitCommand(t, root, append(append([]string{}, config...), "commit", "-qm", "fixture")...)
	if err := os.WriteFile(output, []byte("# Sample\n\nForeign edit.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, root, "add", "instructions.md")
	fragments, err := filepath.Glob(filepath.Join(codex, "frag", "*", "*.md"))
	if err != nil || len(fragments) == 0 {
		t.Fatalf("find fragments: %v, %v", fragments, err)
	}
	fragmentBody, err := os.ReadFile(fragments[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fragments[0], bytes.Replace(fragmentBody, []byte("Original rule."), []byte("Fragment edit."), 1), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("# Sample\n\nDifferent edit.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := captureRunWithStderr(t, "build", "--codex", codex, "--force", output)
	if err != nil {
		t.Fatalf("build --force failed: %v", err)
	}
	if !strings.Contains(stderr, "git: staged and modified") {
		t.Errorf("warning does not name staged and modified output: %q", stderr)
	}
	if !strings.Contains(stderr, "fragments: uncommitted changes in 1 file)") {
		t.Errorf("warning does not name fragment changes: %q", stderr)
	}
}

func TestGuideNotYourFileNamesEditWarning(t *testing.T) {
	guide, err := captureRun(t, "guide")
	if err != nil {
		t.Fatalf("guide failed: %v", err)
	}
	section := strings.SplitN(guide, "## Не твой файл", 2)
	if len(section) != 2 || !strings.Contains(section[1], "warning") {
		t.Errorf("guide section does not describe the edit warning")
	}
}

func TestEditWarningNamesEachGitState(t *testing.T) {
	unsetGitEnvironment(t)
	config := []string{"-c", "user.name=Fixture User", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false"}
	commitAll := func(t *testing.T, root string) {
		gitCommand(t, root, append(append([]string{}, config...), "add", "-A")...)
		gitCommand(t, root, append(append([]string{}, config...), "commit", "-qm", "fixture")...)
	}
	edit := func(t *testing.T, output string) {
		if err := os.WriteFile(output, []byte("# Sample\n\nForeign edit.\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name  string
		setup func(t *testing.T, root, output string)
		want  string
	}{
		{"outside git", func(t *testing.T, root, output string) { edit(t, output) }, "git: not in a git repository; fragments: not in a git repository)"},
		{"outside git, translated git messages", func(t *testing.T, root, output string) {
			t.Setenv("LC_ALL", "ru_RU.UTF-8")
			t.Setenv("LANGUAGE", "ru")
			edit(t, output)
		}, "git: not in a git repository; fragments: not in a git repository)"},
		{"untracked", func(t *testing.T, root, output string) {
			gitCommand(t, root, "init", "-q")
			gitCommand(t, root, append(append([]string{}, config...), "add", "codex")...)
			gitCommand(t, root, append(append([]string{}, config...), "commit", "-qm", "fixture")...)
			edit(t, output)
		}, "git: untracked; fragments: committed)"},
		{"ignored", func(t *testing.T, root, output string) {
			gitCommand(t, root, "init", "-q")
			if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/instructions.md\n"), 0644); err != nil {
				t.Fatal(err)
			}
			commitAll(t, root)
			edit(t, output)
		}, "git: ignored; fragments: committed)"},
		{"committed", func(t *testing.T, root, output string) {
			gitCommand(t, root, "init", "-q")
			edit(t, output)
			commitAll(t, root)
		}, "git: committed; fragments: committed)"},
		{"staged", func(t *testing.T, root, output string) {
			gitCommand(t, root, "init", "-q")
			commitAll(t, root)
			edit(t, output)
			gitCommand(t, root, "add", "instructions.md")
		}, "git: staged; fragments: committed)"},
		{"modified", func(t *testing.T, root, output string) {
			gitCommand(t, root, "init", "-q")
			commitAll(t, root)
			edit(t, output)
		}, "git: modified; fragments: committed)"},
		{"untracked hidden by config", func(t *testing.T, root, output string) {
			gitCommand(t, root, "init", "-q")
			gitCommand(t, root, "config", "status.showUntrackedFiles", "no")
			gitCommand(t, root, append(append([]string{}, config...), "add", "codex")...)
			gitCommand(t, root, append(append([]string{}, config...), "commit", "-qm", "fixture")...)
			edit(t, output)
		}, "git: untracked; fragments: committed)"},
		{"ignored codex", func(t *testing.T, root, output string) {
			gitCommand(t, root, "init", "-q")
			if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("codex/\n"), 0644); err != nil {
				t.Fatal(err)
			}
			commitAll(t, root)
			edit(t, output)
		}, "git: modified; fragments: uncommitted changes in "},
		{"inherited git environment", func(t *testing.T, root, output string) {
			gitCommand(t, root, "init", "-q")
			commitAll(t, root)
			other := t.TempDir()
			gitCommand(t, other, "init", "-q")
			t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
			t.Setenv("GIT_WORK_TREE", other)
			t.Setenv("GIT_INDEX_FILE", filepath.Join(other, "missing-index"))
			edit(t, output)
		}, "git: modified; fragments: committed)"},
		{"git not in PATH", func(t *testing.T, root, output string) {
			gitCommand(t, root, "init", "-q")
			commitAll(t, root)
			edit(t, output)
			t.Setenv("PATH", t.TempDir())
		}, "git: unknown (git not found); fragments: unknown (git not found))"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, codex, output := warningFixture(t)
			tc.setup(t, root, output)
			_, stderr, err := captureRunWithStderr(t, "build", "--codex", codex, "--force", output)
			if err != nil {
				t.Fatalf("build --force failed: %v", err)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("warning %q does not contain %q", stderr, tc.want)
			}
		})
	}
}

func TestEditWarningCountsChangedFragmentFiles(t *testing.T) {
	unsetGitEnvironment(t)
	root, codex, output := warningFixture(t)
	gitCommand(t, root, "init", "-q")
	config := []string{"-c", "user.name=Fixture User", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false"}
	gitCommand(t, root, append(append([]string{}, config...), "add", "-A")...)
	gitCommand(t, root, append(append([]string{}, config...), "commit", "-qm", "fixture")...)
	fragments, err := filepath.Glob(filepath.Join(codex, "frag", "*", "*.md"))
	if err != nil || len(fragments) < 2 {
		t.Fatalf("fixture needs two fragments: %v, %v", fragments, err)
	}
	for _, path := range fragments[:2] {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if writeErr := os.WriteFile(path, append(data, []byte("Extra line.\n")...), 0644); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if err := os.WriteFile(output, []byte("# Sample\n\nForeign edit.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := captureRunWithStderr(t, "build", "--codex", codex, "--force", output)
	if err != nil {
		t.Fatalf("build --force failed: %v", err)
	}
	if !strings.Contains(stderr, "fragments: uncommitted changes in 2 files)") {
		t.Errorf("warning does not count two changed fragment files: %q", stderr)
	}
}

func TestAbsorbForceWithUneditedFileDoesNotWarn(t *testing.T) {
	_, codex, output := warningFixture(t)
	fragments, err := filepath.Glob(filepath.Join(codex, "frag", "*", "*.md"))
	if err != nil || len(fragments) == 0 {
		t.Fatalf("find fragments: %v, %v", fragments, err)
	}
	for _, path := range fragments {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if bytes.Contains(data, []byte("Original rule.")) {
			if writeErr := os.WriteFile(path, bytes.Replace(data, []byte("Original rule."), []byte("Fragment edit."), 1), 0644); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
	}
	_, stderr, err := captureRunWithStderr(t, "absorb", "--codex", codex, "--force", output)
	if err != nil {
		t.Fatalf("absorb --force failed: %v", err)
	}
	if strings.Contains(stderr, "warning:") {
		t.Errorf("absorb --force of an unedited file warned: %q", stderr)
	}
	built, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(built, []byte("Original rule.")) {
		t.Errorf("absorb --force changed the file: %q", built)
	}
}

func TestAbsorbRefusalDoesNotWarn(t *testing.T) {
	_, codex, output := warningFixture(t)
	if err := os.WriteFile(output, []byte("# Changed\nChanged rule.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := captureRunWithStderr(t, "absorb", "--codex", codex, output)
	if err == nil {
		t.Fatalf("absorb of an edit across two fragments was not refused: %q", stderr)
	}
	if strings.Contains(stderr, "warning:") || strings.Contains(err.Error(), "warning:") {
		t.Errorf("refused absorb warned: %q, %v", stderr, err)
	}
}

func TestEditWarningTakesPathsLiterally(t *testing.T) {
	unsetGitEnvironment(t)
	config := []string{"-c", "user.name=Fixture User", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false"}
	cases := []struct {
		name, file, neighbor string
		commitEdit           bool
		want                 string
	}{
		{"leading colon", ":notes.md", "", false, "git: modified; fragments: committed)"},
		{"glob character", "notes?.md", "notes1.md", true, "git: committed; fragments: committed)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			codex := filepath.Join(root, "codex")
			output := filepath.Join(root, tc.file)
			if err := os.WriteFile(output, []byte("# Sample\n\nOriginal rule.\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := lex.ImportWithOptions(codex, output, lex.ImportOptions{Home: root}); err != nil {
				t.Fatalf("import fixture: %v", err)
			}
			if err := lex.Build(codex, false); err != nil {
				t.Fatalf("build fixture: %v", err)
			}
			if tc.neighbor != "" {
				if err := os.WriteFile(filepath.Join(root, tc.neighbor), []byte("neighbor\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			gitCommand(t, root, "init", "-q")
			gitCommand(t, root, append(append([]string{}, config...), "add", "-A")...)
			gitCommand(t, root, append(append([]string{}, config...), "commit", "-qm", "fixture")...)
			if err := os.WriteFile(output, []byte("# Sample\n\nForeign edit.\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if tc.commitEdit {
				gitCommand(t, root, append(append([]string{}, config...), "commit", "-qam", "edit")...)
			}
			if tc.neighbor != "" {
				if err := os.WriteFile(filepath.Join(root, tc.neighbor), []byte("neighbor edit\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			_, stderr, err := captureRunWithStderr(t, "build", "--codex", codex, "--force", output)
			if err != nil {
				t.Fatalf("build --force failed: %v", err)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("warning %q does not contain %q", stderr, tc.want)
			}
		})
	}
}
