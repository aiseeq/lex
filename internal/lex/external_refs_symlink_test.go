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

// A relative reference resolves from each entry's own directory, so two entries for one file in
// different directories keep the finding that only one of them has, in either order.
func TestExternalEntriesForOneFileKeepRelativeFindings(t *testing.T) {
	for _, tc := range []struct {
		name, present, missing string
		aliasFirst             bool
	}{
		{"real file's link broken, alias first", "notes.md", "sub/notes.md", true},
		{"real file's link broken, real first", "notes.md", "sub/notes.md", false},
		{"alias's link broken, alias first", "sub/notes.md", "notes.md", true},
		{"alias's link broken, real first", "sub/notes.md", "notes.md", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			codex := filepath.Join(home, "codex")
			externalRefsImport(t, codex, filepath.Join(home, "tree", "CLAUDE.md"), "CLAUDE.md", "# Бюджет\n")
			if err := Build(codex, false); err != nil {
				t.Fatalf("build corpus: %v", err)
			}
			agent := filepath.Join(home, "sub", "agent.md")
			if err := os.MkdirAll(filepath.Dir(agent), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(agent, []byte("См. [заметки](notes.md).\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, filepath.FromSlash(tc.present)), []byte("x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(agent, filepath.Join(home, "alias.md")); err != nil {
				t.Fatal(err)
			}
			entries := []string{`"~/alias.md"`, `"` + agent + `"`}
			if !tc.aliasFirst {
				entries[0], entries[1] = entries[1], entries[0]
			}
			tools := `{"text_refs":{"external":[` + strings.Join(entries, ",") + `]}}`
			if err := os.WriteFile(filepath.Join(codex, "tools.json"), []byte(tools), 0o644); err != nil {
				t.Fatal(err)
			}
			warnings, err := CheckWithWarnings(codex, CheckOptions{})
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			var notes []string
			for _, warning := range warnings {
				if strings.Contains(warning, "notes.md") {
					notes = append(notes, warning)
				}
			}
			want := filepath.Join(home, filepath.FromSlash(tc.missing))
			if len(notes) != 1 || !strings.HasSuffix(notes[0], want) {
				t.Errorf("want one broken link to %s, got %v", want, notes)
			}
		})
	}
}
