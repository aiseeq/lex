package lex

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initMissingDiskRepo(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-q", "-m", "initial"}} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
	}
}

func runMissingDiskGit(t *testing.T, root string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, output, err)
	}
	return output
}

func stageMissingDiskFile(t *testing.T, root, path string) {
	t.Helper()
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", root, "add", rel)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add %s: %s: %v", rel, output, err)
	}
}

func TestCheckNamesManifestMissingOnDisk(t *testing.T) {
	root, codex, _, _ := sharedEditFixture(t)
	t.Chdir(root)
	initMissingDiskRepo(t, root)
	manifests, err := filepath.Glob(filepath.Join(codex, "manifest", "*.yaml"))
	if err != nil || len(manifests) != 2 {
		t.Fatalf("find manifests: %v, %v", manifests, err)
	}
	manifest := manifests[0]
	if err := os.Remove(manifest); err != nil {
		t.Fatal(err)
	}
	_, err = CheckWithWarnings(codex, CheckOptions{})
	if err == nil || !strings.Contains(err.Error(), manifest) || !strings.Contains(err.Error(), "checkout --") || !strings.Contains(strings.ToLower(err.Error()), "fragment") {
		t.Fatalf("check advice should name and restore missing manifest %s and explain its fragments, got %v", manifest, err)
	}
}

func TestCheckMissingManifestIntentionalDeletion(t *testing.T) {
	for _, both := range []bool{false, true} {
		t.Run(map[bool]string{false: "one", true: "both"}[both], func(t *testing.T) {
			root, codex, _, _ := sharedEditFixture(t)
			t.Chdir(root)
			initMissingDiskRepo(t, root)
			manifests, err := filepath.Glob(filepath.Join(codex, "manifest", "*.yaml"))
			if err != nil || len(manifests) != 2 {
				t.Fatalf("find manifests: %v, %v", manifests, err)
			}
			keep := map[string]bool{}
			if !both {
				other, err := readManifest(manifests[1])
				if err != nil {
					t.Fatal(err)
				}
				for _, id := range other.parts {
					keep[id] = true
				}
			}
			for _, manifestPath := range manifests {
				m, err := readManifest(manifestPath)
				if err != nil {
					t.Fatal(err)
				}
				if both || strings.Contains(manifestPath, codexKey("docs/a.md")) {
					if err := os.Remove(manifestPath); err != nil {
						t.Fatal(err)
					}
					for _, id := range m.parts {
						if both || !keep[id] {
							err := os.Remove(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
							if err != nil && !os.IsNotExist(err) {
								t.Fatal(err)
							}
						}
					}
				}
			}
			if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
				t.Fatalf("intentional manifest and fragment deletion should pass: %v", err)
			}
		})
	}
}

func TestCheckMissingFragmentRestoreCommands(t *testing.T) {
	for _, tc := range []struct {
		name            string
		removeFromIndex bool
		outside         bool
	}{
		{name: "index"}, {name: "HEAD", removeFromIndex: true}, {name: "outside repository", outside: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, codex, _, _ := sharedEditFixture(t)
			initMissingDiskRepo(t, root)
			t.Chdir(root)
			m := mustReadManifest(t, filepath.Join(codex, "manifest", codexKey("docs/a.md")+".yaml"))
			fragment := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[1])+".md")
			if err := os.Remove(fragment); err != nil {
				t.Fatal(err)
			}
			if tc.removeFromIndex {
				rel, err := filepath.Rel(root, fragment)
				if err != nil {
					t.Fatal(err)
				}
				runMissingDiskGit(t, root, "rm", "--", rel)
			}
			if tc.outside {
				t.Chdir(t.TempDir())
			}
			_, err := CheckWithWarnings(codex, CheckOptions{})
			if err == nil {
				t.Fatal("check unexpectedly passed")
			}
			want := "git -C " + ShellQuoteArg(root) + " checkout --"
			if tc.removeFromIndex {
				want = "git -C " + ShellQuoteArg(root) + " checkout HEAD --"
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("restore advice lacks %q: %v", want, err)
			}
		})
	}
}

func TestCheckMissingFragmentWithoutGit(t *testing.T) {
	_, codex, _, _ := sharedEditFixture(t)
	m := mustReadManifest(t, filepath.Join(codex, "manifest", codexKey("docs/a.md")+".yaml"))
	fragment := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[1])+".md")
	if err := os.Remove(fragment); err != nil {
		t.Fatal(err)
	}
	_, err := CheckWithWarnings(codex, CheckOptions{})
	if err == nil || !strings.Contains(err.Error(), "restore it from a backup") || strings.Contains(err.Error(), "git checkout") {
		t.Fatalf("non-git advice = %v", err)
	}

	root, codex, _, _ := sharedEditFixture(t)
	initMissingDiskRepo(t, root)
	m = mustReadManifest(t, filepath.Join(codex, "manifest", codexKey("docs/a.md")+".yaml"))
	fragment = filepath.Join(codex, "frag", filepath.FromSlash(m.parts[1])+".md")
	if err := os.Remove(fragment); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	_, err = CheckWithWarnings(codex, CheckOptions{})
	if err == nil || strings.Contains(err.Error(), "find git root") || strings.Contains(err.Error(), "executable file not found") {
		t.Fatalf("check without git executable should return missing-fragment advice, got %v", err)
	}
}

func TestCheckMissingFragmentAdviceUnderOutputs(t *testing.T) {
	for _, treeMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "outputs-under", true: "tree"}[treeMode], func(t *testing.T) {
			_, codex, a, b := sharedEditFixture(t)
			data, err := os.ReadFile(a)
			if err != nil {
				t.Fatal(err)
			}
			mustWrite(t, a, []byte(strings.Replace(string(data), "shared para", "changed shared para", 1)))
			m := mustReadManifest(t, filepath.Join(codex, "manifest", codexKey("docs/a.md")+".yaml"))
			fragment := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[1])+".md")
			if err := os.Remove(fragment); err != nil {
				t.Fatal(err)
			}
			opts := CheckOptions{OutputsUnder: filepath.Dir(a)}
			if treeMode {
				tree := filepath.Join(t.TempDir(), "tree")
				treeDir := filepath.Join(tree, filepath.Base(filepath.Dir(a)))
				if err := os.MkdirAll(treeDir, directoryMode); err != nil {
					t.Fatal(err)
				}
				for _, output := range []string{a, b} {
					content, err := os.ReadFile(output)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(treeDir, filepath.Base(output)), content, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				opts.Tree = tree
			}
			_, err = CheckWithWarnings(codex, opts)
			if err == nil || !strings.Contains(err.Error(), "missing fragment") || strings.Contains(err.Error(), "read fragment for absorb") || strings.Contains(err.Error(), "no such file or directory") {
				t.Fatalf("scoped check advice = %v", err)
			}
		})
	}
}

func TestCheckMissingFragmentAdvice(t *testing.T) {
	for _, staged := range []bool{false, true} {
		t.Run(map[bool]string{false: "disk", true: "staged"}[staged], func(t *testing.T) {
			root, codex, a, _ := sharedEditFixture(t)
			t.Chdir(root)
			initMissingDiskRepo(t, root)
			mPath := filepath.Join(codex, "manifest", codexKey("docs/a.md")+".yaml")
			m := mustReadManifest(t, mPath)
			shared := m.parts[1]
			fragment := filepath.Join(codex, "frag", filepath.FromSlash(shared)+".md")
			if err := os.Remove(fragment); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(a)
			if err != nil {
				t.Fatal(err)
			}
			mustWrite(t, a, []byte(strings.Replace(string(data), "shared para", "changed shared para", 1)))
			if staged {
				stageMissingDiskFile(t, root, a)
			}
			_, err = CheckWithWarnings(codex, CheckOptions{Staged: staged})
			if err == nil || !strings.Contains(err.Error(), shared) || !strings.Contains(err.Error(), mPath) || strings.Contains(err.Error(), a) || (!strings.Contains(strings.ToLower(err.Error()), "restore") && !strings.Contains(strings.ToLower(err.Error()), "remove")) || strings.Contains(err.Error(), "no such file or directory") || strings.Contains(err.Error(), "read fragment for absorb") || strings.Contains(err.Error(), "git add it together") {
				t.Fatalf("check advice for missing fragment %s = %v", shared, err)
			}
		})
	}
}
