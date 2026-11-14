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
	if err == nil || !strings.Contains(err.Error(), manifest) || !strings.Contains(err.Error(), "git checkout --") || !strings.Contains(strings.ToLower(err.Error()), "fragment") {
		t.Fatalf("check advice should name and restore missing manifest %s and explain its fragments, got %v", manifest, err)
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
			if err == nil || !strings.Contains(err.Error(), shared) || (!strings.Contains(strings.ToLower(err.Error()), "restore") && !strings.Contains(strings.ToLower(err.Error()), "remove")) || strings.Contains(err.Error(), "no such file or directory") || strings.Contains(err.Error(), "read fragment for absorb") || strings.Contains(err.Error(), "git add it together") {
				t.Fatalf("check advice for missing fragment %s = %v", shared, err)
			}
		})
	}
}
