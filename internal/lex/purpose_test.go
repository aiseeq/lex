package lex

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func purposeFixture(t *testing.T, input string) (string, string, string, manifest) {
	t.Helper()
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.md")
	if err := importData(codex, out, "out.md", []byte(input), false); err != nil {
		t.Fatal(err)
	}
	mp := filepath.Join(codex, "manifest", "out.md.yaml")
	m, err := readManifest(mp)
	if err != nil {
		t.Fatal(err)
	}
	return codex, out, mp, m
}

func purposeSet(t *testing.T, codex string, enabled bool) {
	t.Helper()
	value := `{"print":false}`
	if enabled {
		value = `{"print":true}`
	}
	if err := os.WriteFile(filepath.Join(codex, "purpose.json"), []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}

func purposeFragment(t *testing.T, codex, mp string, m manifest, part int, goal string) {
	t.Helper()
	path := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[part])+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("цель: \n"), []byte("цель: "+goal+"\n"), 1)
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	m.fragmentHashes[m.parts[part]] = sha(raw)
	if err := writeManifest(mp, m, m.links); err != nil {
		t.Fatal(err)
	}
}

func purposeRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPurposeOffUnchanged(t *testing.T) {
	c, out, mp, m := purposeFixture(t, "- one\n- two\n")
	purposeFragment(t, c, mp, m, 0, "avoid failure")
	for _, setting := range []int{0, 1} {
		if setting == 1 {
			purposeSet(t, c, false)
		}
		if err := Build(c, false); err != nil {
			t.Fatal(err)
		}
		if got := purposeRead(t, out); got != "- one\n- two\n" {
			t.Fatalf("output %q", got)
		}
	}
}

func TestPurposeForms(t *testing.T) {
	for _, input := range []string{"- one\n", "1. one\n", "paragraph\n", "- one\n  - nested\n", "- one\n\n"} {
		t.Run(strings.TrimSpace(input), func(t *testing.T) {
			c, out, mp, m := purposeFixture(t, input)
			purposeFragment(t, c, mp, m, 0, "reason")
			purposeSet(t, c, true)
			if err := Build(c, false); err != nil {
				t.Fatal(err)
			}
			want := strings.TrimRight(input, "\n") + "\n" + purposeMarker + "reason\n" + strings.Repeat("\n", len(input)-len(strings.TrimRight(input, "\n"))-1)
			if got := purposeRead(t, out); got != want {
				t.Fatalf("got %q want %q", got, want)
			}
		})
	}
}

func TestPurposeNotPrinted(t *testing.T) {
	for _, input := range []string{"# heading\n", "```\ncode\n```\n", "---\ntitle: x\n---\n"} {
		c, out, mp, m := purposeFixture(t, input)
		purposeFragment(t, c, mp, m, 0, "reason")
		purposeSet(t, c, true)
		if err := Build(c, false); err != nil {
			t.Fatal(err)
		}
		if got := purposeRead(t, out); got != input {
			t.Fatalf("got %q", got)
		}
		warnings, err := CheckWithWarnings(c, CheckOptions{})
		if err != nil || !strings.Contains(strings.Join(warnings, "\n"), "purpose is not printed") {
			t.Fatalf("warnings %v error %v", warnings, err)
		}
	}
}

func TestPurposeChecks(t *testing.T) {
	c, out, mp, m := purposeFixture(t, "- one\n")
	purposeFragment(t, c, mp, m, 0, "reason")
	purposeSet(t, c, true)
	if err := Build(c, false); err != nil {
		t.Fatal(err)
	}
	if err := Build(c, true); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckWithWarnings(c, CheckOptions{}); err != nil {
		t.Fatal(err)
	}
	tree := t.TempDir()
	if err := os.WriteFile(filepath.Join(tree, filepath.Base(out)), []byte(purposeRead(t, out)), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckWithWarnings(c, CheckOptions{OutputsUnder: filepath.Dir(out), Tree: tree}); err != nil {
		t.Fatal(err)
	}
	purposeSet(t, c, false)
	if err := Build(c, false); err != nil {
		t.Fatal(err)
	}
	if got := purposeRead(t, out); got != "- one\n" {
		t.Fatalf("rollback %q", got)
	}
}

func TestPurposeAbsorb(t *testing.T) {
	c, out, mp, m := purposeFixture(t, "- one\n- two\n")
	purposeFragment(t, c, mp, m, 0, "reason")
	purposeSet(t, c, true)
	if err := Build(c, false); err != nil {
		t.Fatal(err)
	}
	content := strings.Replace(purposeRead(t, out), "- one", "- edited", 1)
	if err := os.WriteFile(out, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Absorb(c, out, AbsorbOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := Build(c, true); err != nil {
		t.Fatal(err)
	}
	content = strings.Replace(content, purposeMarker+"reason\n", "", 1)
	if err := os.WriteFile(out, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Absorb(c, out, AbsorbOptions{}); err == nil {
		t.Fatal("missing purpose accepted")
	}
}

func TestPurposeRollback(t *testing.T) {
	c, out, mp, m := purposeFixture(t, "- one\n")
	purposeFragment(t, c, mp, m, 0, "reason")
	purposeSet(t, c, true)
	if err := Build(c, false); err != nil {
		t.Fatal(err)
	}
	purposeSet(t, c, false)
	if err := Absorb(c, out, AbsorbOptions{}); err == nil {
		t.Fatal("absorb accepted stale purpose")
	}
	if err := importData(c, out, "out.md", []byte(purposeRead(t, out)), true); err == nil {
		t.Fatal("refresh accepted stale purpose")
	}
	if err := Build(c, false); err != nil {
		t.Fatal(err)
	}
	if got := purposeRead(t, out); got != "- one\n" {
		t.Fatalf("got %q", got)
	}
}

func TestPurposeRefresh(t *testing.T) {
	c, out, mp, m := purposeFixture(t, "- one\n- two\n")
	purposeFragment(t, c, mp, m, 0, "reason")
	purposeSet(t, c, true)
	if err := Build(c, false); err != nil {
		t.Fatal(err)
	}
	data := []byte(strings.Replace(purposeRead(t, out), "- one", "- changed", 1))
	if err := os.WriteFile(out, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := importData(c, out, "out.md", data, true); err != nil {
		t.Fatal(err)
	}
	if err := Build(c, true); err != nil {
		t.Fatal(err)
	}
}

func TestPurposeStaged(t *testing.T) {
	root := t.TempDir()
	c, out := filepath.Join(root, "codex"), filepath.Join(root, "out.md")
	if err := importData(c, out, "out.md", []byte("- one\n"), false); err != nil {
		t.Fatal(err)
	}
	mp := filepath.Join(c, "manifest", "out.md.yaml")
	m, err := readManifest(mp)
	if err != nil {
		t.Fatal(err)
	}
	purposeFragment(t, c, mp, m, 0, "reason")
	if err := Build(c, false); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if result, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, result, err)
		}
	}
	git("init", "-q")
	git("add", ".")
	git("-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-qm", "initial")
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	purposeSet(t, c, true)
	git("add", "codex/purpose.json")
	if _, err := CheckWithWarnings(c, CheckOptions{Staged: true}); err == nil || !strings.Contains(err.Error(), out) {
		t.Fatalf("missing staged output error: %v", err)
	}
	if err := Build(c, false); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	purposeSet(t, c, false)
	if _, err := CheckWithWarnings(c, CheckOptions{Staged: true}); err != nil {
		t.Fatal(err)
	}
}

func TestPurposeQuotes(t *testing.T) {
	c, out, mp, m := purposeFixture(t, "Rule (пользователь 03.02: «word»).\n")
	purposeFragment(t, c, mp, m, 0, "reason")
	purposeSet(t, c, true)
	if err := Build(c, false); err != nil {
		t.Fatal(err)
	}
	if err := QuotesToBasis(c, QuotesToBasisOptions{Apply: true, Report: filepath.Join(t.TempDir(), "report.json")}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(purposeRead(t, out), purposeMarker+"reason") {
		t.Fatal("purpose lost")
	}
	if _, err := CheckWithWarnings(c, CheckOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestPurposeToolCheck(t *testing.T) {
	tool := loadedTool{loaded: true, config: configuredTool{Name: "graft"}, tasksKnown: true, registry: toolRegistry{Commands: []toolCommand{{Path: []string{"commit"}}}}}
	raw := []byte("---\nid: example\nцель: `graft nonexistent`\nоснование: \nснять_когда: \n---\n- rule\n")
	checker := toolChecker{tools: []loadedTool{tool}, seen: map[string]bool{}, purpose: true}
	warnings, problems := splitToolCheck(&checker, "example", raw)
	if len(problems) != 0 || !strings.Contains(strings.Join(warnings, "\n"), "nonexistent") {
		t.Fatalf("missing tool warning: warnings=%q problems=%v", warnings, problems)
	}
	checker = toolChecker{tools: []loadedTool{tool}, seen: map[string]bool{}}
	warnings, problems = splitToolCheck(&checker, "example", raw)
	if len(warnings)+len(problems) != 0 {
		t.Fatalf("unexpected results: warnings=%v problems=%v", warnings, problems)
	}
}

func TestPurposeAbsorbCases(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(string) string
		ok   bool
	}{
		{"remove", func(s string) string { return strings.Replace(s, "- one\n"+purposeMarker+"reason\n", "", 1) }, true},
		{"insert", func(s string) string {
			return strings.Replace(s, purposeMarker+"reason\n", purposeMarker+"reason\n- inserted\n", 1)
		}, true},
		{"edit goal", func(s string) string { return strings.Replace(s, purposeMarker+"reason", purposeMarker+"changed", 1) }, false},
		{"new goal", func(s string) string { return s + purposeMarker + "new\n" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, out, mp, m := purposeFixture(t, "- one\n- two\n")
			purposeFragment(t, c, mp, m, 0, "reason")
			purposeSet(t, c, true)
			if err := Build(c, false); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(out, []byte(tc.edit(purposeRead(t, out))), 0644); err != nil {
				t.Fatal(err)
			}
			err := Absorb(c, out, AbsorbOptions{})
			if (err == nil) != tc.ok {
				t.Fatalf("absorb: %v", err)
			}
			if err == nil {
				if err := Build(c, true); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestPurposeAbsorbMovedGoal(t *testing.T) {
	c, out, mp, m := purposeFixture(t, "- one\n- two\n")
	purposeFragment(t, c, mp, m, 1, "reason")
	purposeSet(t, c, true)
	if err := Build(c, false); err != nil {
		t.Fatal(err)
	}
	before := treeBytes(t, c)
	content := strings.Replace(purposeRead(t, out), "- one\n- two\n"+purposeMarker+"reason\n", "- one\n"+purposeMarker+"reason\n- two\n", 1)
	if err := os.WriteFile(out, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	err := Absorb(c, out, AbsorbOptions{})
	if err == nil || !strings.Contains(err.Error(), m.parts[1]) || !strings.Contains(err.Error(), "цель moved to another fragment") {
		t.Fatalf("moved purpose: %v", err)
	}
	if after := treeBytes(t, c); !bytes.Equal(before, after) {
		t.Fatal("failed absorb changed codex")
	}
}

func TestPurposeDuplicate(t *testing.T) {
	c, _, _, m := purposeFixture(t, "- one\n"+purposeMarker+"manual\n")
	purposeSet(t, c, true)
	if err := Build(c, false); err == nil || !strings.Contains(err.Error(), m.parts[0]) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := CheckWithWarnings(c, CheckOptions{}); err == nil {
		t.Fatal("check accepted duplicate")
	}
	purposeSet(t, c, false)
	if err := Build(c, false); err != nil {
		t.Fatal(err)
	}
}

func TestPurposeRefreshMissing(t *testing.T) {
	c, out, mp, m := purposeFixture(t, "- one\n")
	purposeFragment(t, c, mp, m, 0, "reason")
	purposeSet(t, c, true)
	before := treeBytes(t, c)
	if err := importData(c, out, "out.md", []byte("- one\n"), true); err == nil || !strings.Contains(err.Error(), "цель") {
		t.Fatalf("refresh: %v", err)
	}
	if after := treeBytes(t, c); !bytes.Equal(before, after) {
		t.Fatal("failed refresh changed codex")
	}
}

func purposeManualID(t *testing.T, c, mp string, m manifest) manifest {
	t.Helper()
	old := m.parts[0]
	newID := "out.md/manual-id"
	oldPath := filepath.Join(c, "frag", filepath.FromSlash(old)+".md")
	newPath := filepath.Join(c, "frag", filepath.FromSlash(newID)+".md")
	raw := []byte(strings.Replace(purposeRead(t, oldPath), "id: "+old, "id: "+newID, 1))
	if err := os.WriteFile(newPath, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(oldPath); err != nil {
		t.Fatal(err)
	}
	m.parts[0] = newID
	delete(m.fragmentHashes, old)
	m.fragmentHashes[newID] = sha(raw)
	if err := writeManifest(mp, m, m.links); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPurposeRefreshManualID(t *testing.T) {
	c, out, mp, m := purposeFixture(t, "- one\n- two\n")
	purposeFragment(t, c, mp, m, 0, "reason")
	purposeManualID(t, c, mp, m)
	purposeSet(t, c, true)
	if err := Build(c, false); err != nil {
		t.Fatal(err)
	}
	data := []byte(strings.Replace(purposeRead(t, out), "- one", "- changed", 1))
	if err := os.WriteFile(out, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := importData(c, out, "out.md", data, true); err != nil {
		t.Fatal(err)
	}
	if err := Build(c, true); err != nil {
		t.Fatal(err)
	}
	updated, err := readManifest(mp)
	if err != nil {
		t.Fatal(err)
	}
	fragment := purposeRead(t, filepath.Join(c, "frag", filepath.FromSlash(updated.parts[0])+".md"))
	if !strings.Contains(fragment, "цель: reason\n") || strings.Contains(fragment, purposeMarker) {
		t.Fatalf("bad fragment %q", fragment)
	}
}

func TestPurposeRefreshManualIDAndMissingHash(t *testing.T) {
	c, out, mp, m := purposeFixture(t, "- one\n- two\n")
	purposeFragment(t, c, mp, m, 0, "reason")
	m = purposeManualID(t, c, mp, m)
	delete(m.fragmentHashes, m.parts[0])
	if err := writeManifest(mp, m, m.links); err != nil {
		t.Fatal(err)
	}
	purposeSet(t, c, true)
	if err := Build(c, false); err != nil {
		t.Fatal(err)
	}
	data := []byte(strings.Replace(purposeRead(t, out), "- one", "- changed", 1))
	if err := os.WriteFile(out, data, 0644); err != nil {
		t.Fatal(err)
	}
	before := treeBytes(t, c)
	err := importData(c, out, "out.md", data, true)
	if err == nil || !strings.Contains(err.Error(), m.parts[0]) || !strings.Contains(err.Error(), "fragment body was edited") {
		t.Fatalf("refresh without fragment hash: %v", err)
	}
	if after := treeBytes(t, c); !bytes.Equal(before, after) {
		t.Fatal("failed refresh changed manifest or fragments")
	}
}

func TestPurposeRefreshAmbiguousGoal(t *testing.T) {
	c, out, mp, m := purposeFixture(t, "- one\n- two\n")
	purposeFragment(t, c, mp, m, 0, "same reason")
	purposeFragment(t, c, mp, m, 1, "same reason")
	purposeSet(t, c, true)
	if err := Build(c, false); err != nil {
		t.Fatal(err)
	}
	data := strings.Replace(purposeRead(t, out), "- one\n", "- changed one\n", 1)
	data = strings.Replace(data, "- two\n", "- changed two\n", 1)
	if err := os.WriteFile(out, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	before := treeBytes(t, c)
	err := importData(c, out, "out.md", []byte(data), true)
	if err == nil || !strings.Contains(err.Error(), "ambiguous цель") {
		t.Fatalf("refresh with ambiguous purpose: %v", err)
	}
	if after := treeBytes(t, c); !bytes.Equal(before, after) {
		t.Fatal("failed refresh changed manifest or fragments")
	}
}
