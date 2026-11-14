package lex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An external entry that reaches a corpus file through a symlinked directory is that corpus file:
// it is not read a second time, and another external file's section reference to it is still checked.
func TestExternalEntryThroughSymlinkIsCorpusFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codex := filepath.Join(home, "codex")
	tree := filepath.Join(home, "tree")
	output := filepath.Join(tree, "CLAUDE.md")
	externalRefsImport(t, codex, output, "CLAUDE.md", "# Бюджет\n\nСм. раздел «Пропуск» в `~/tree/CLAUDE.md`.\n")
	if err := Build(codex, false); err != nil {
		t.Fatalf("build corpus: %v", err)
	}
	if err := os.Symlink(tree, filepath.Join(home, "link")); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(home, "agent.md")
	if err := os.WriteFile(agent, []byte("- Расходы — раздел «Бюджет» в `~/link/CLAUDE.md`.\n- Доходы — раздел «Деньги» в `~/link/CLAUDE.md`.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tools := `{"text_refs":{"external":["~/link/CLAUDE.md","` + agent + `"]}}`
	if err := os.WriteFile(filepath.Join(codex, "tools.json"), []byte(tools), 0o644); err != nil {
		t.Fatal(err)
	}
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	var own, money, budget int
	for _, warning := range warnings {
		if strings.Contains(warning, filepath.Join(home, "link")) {
			t.Errorf("corpus file read again through the symlink: %s", warning)
		}
		if strings.Contains(warning, "«Пропуск»") {
			own++
		}
		if strings.Contains(warning, agent) && strings.Contains(warning, "«Деньги»") {
			money++
		}
		if strings.Contains(warning, "«Бюджет»") {
			budget++
		}
	}
	if own != 1 {
		t.Errorf("want the corpus file's own finding once, got %d in %v", own, warnings)
	}
	if money != 1 {
		t.Errorf("want the external file's missing section «Деньги» reported once, got %d in %v", money, warnings)
	}
	if budget != 0 {
		t.Errorf("existing section «Бюджет» reported as missing: %v", warnings)
	}
}

// Two external entries that reach one file outside the corpus, one through a symlink, read it once.
func TestExternalEntriesForOneFileReadItOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codex := filepath.Join(home, "codex")
	output := filepath.Join(home, "tree", "CLAUDE.md")
	externalRefsImport(t, codex, output, "CLAUDE.md", "# Бюджет\n")
	if err := Build(codex, false); err != nil {
		t.Fatalf("build corpus: %v", err)
	}
	agent := filepath.Join(home, "agent.md")
	if err := os.WriteFile(agent, []byte("- Доходы — раздел «Деньги» в `~/tree/CLAUDE.md`.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(agent, filepath.Join(home, "alias.md")); err != nil {
		t.Fatal(err)
	}
	tools := `{"text_refs":{"external":["` + agent + `","~/alias.md"]}}`
	if err := os.WriteFile(filepath.Join(codex, "tools.json"), []byte(tools), 0o644); err != nil {
		t.Fatal(err)
	}
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	var money []string
	for _, warning := range warnings {
		if strings.Contains(warning, "«Деньги»") {
			money = append(money, warning)
		}
	}
	if len(money) != 1 || !strings.HasPrefix(money[0], agent+":") {
		t.Errorf("want the missing section «Деньги» once, under %s; got %v", agent, money)
	}
}
