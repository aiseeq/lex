package lex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func externalRefsTools(t *testing.T, codex, external string) {
	t.Helper()
	data := `{"text_refs":{"external":[` + `"` + external + `"` + `]}}`
	if err := os.WriteFile(filepath.Join(codex, "tools.json"), []byte(data), 0644); err != nil {
		t.Fatalf("write tools.json: %v", err)
	}
}

func externalRefsImport(t *testing.T, codex, output, rel, body string) {
	t.Helper()
	if err := importData(codex, output, rel, []byte(body), false); err != nil {
		t.Fatalf("import %s: %v", rel, err)
	}
}

func TestExternalFileSectionRefWarns(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	output := filepath.Join(root, "CLAUDE.md")
	external := filepath.Join(root, "agent.md")
	externalRefsImport(t, codex, output, "CLAUDE.md", "# Бюджет\n")
	if err := os.WriteFile(external, []byte("- Расходы — раздел «Деньги» в `"+output+"`.\n"), 0644); err != nil {
		t.Fatalf("write external file: %v", err)
	}
	externalRefsTools(t, codex, external)
	if err := Build(codex, false); err != nil {
		t.Fatalf("build corpus: %v", err)
	}
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	found := false
	for _, warning := range warnings {
		if strings.Contains(warning, external) && strings.Contains(warning, "Деньги") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected external section warning at %s; got %v", external, warnings)
	}
}

func TestAbsoluteAndHomeFileRefsResolve(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	absTarget := filepath.Join(home, "absolute.md")
	homeTarget := filepath.Join(home, "home.md")
	source := filepath.Join(home, "source.md")
	corpus := textCorpus{
		absTarget:  []byte("# Другое\n"),
		homeTarget: []byte("# Другое\n"),
		source:     []byte("Ссылка: раздел «X» в `" + absTarget + "`.\nСсылка: раздел «Y» в `~/home.md`.\n"),
	}
	findings := findTextRefs(corpus, filepath.Join(home, "decisions.md"))
	seen := map[string]bool{}
	for _, finding := range findings {
		if finding.kind == 2 {
			seen[finding.missing] = true
		}
	}
	if !seen["нет раздела или пункта «X» в "+absTarget] {
		t.Errorf("absolute reference did not resolve: %v", findings)
	}
	if !seen["нет раздела или пункта «Y» в "+homeTarget] {
		t.Errorf("home reference did not resolve: %v", findings)
	}
}

func TestPlainLineHeadingCountsAsSection(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.md")
	target := filepath.Join(dir, "x.md")
	corpus := textCorpus{
		source: []byte("Ссылка: раздел «Факты и время» в `x.md`.\n"),
		target: []byte("Факты и время\n"),
	}
	if findings := findTextRefs(corpus, filepath.Join(dir, "decisions.md")); len(findings) != 0 {
		t.Errorf("plain line heading should resolve the section: %v", findings)
	}
}

func TestExternalFilesNotListedAreNotRead(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	output := filepath.Join(root, "CLAUDE.md")
	external := filepath.Join(root, "unlisted.md")
	externalRefsImport(t, codex, output, "CLAUDE.md", "# Бюджет\n")
	if err := os.WriteFile(external, []byte("Ссылка на раздел «Нет» в `missing.md`.\n"), 0644); err != nil {
		t.Fatalf("write unlisted file: %v", err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatalf("build corpus: %v", err)
	}
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, warning := range warnings {
		if strings.Contains(warning, external) {
			t.Errorf("unlisted external file was scanned: %v", warnings)
		}
	}
}

// A file listed in text_refs.external exists on disk: a reference to it is not a missing file.
func TestReferenceToExternalFileIsNotMissing(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	output := filepath.Join(root, "CLAUDE.md")
	external := filepath.Join(root, "agent.md")
	other := filepath.Join(root, "helper.md")
	externalRefsImport(t, codex, output, "CLAUDE.md", "# Бюджет\n")
	for path, body := range map[string]string{external: "Подробнее — `helper.md` рядом.\n", other: "Подробнее — `agent.md` рядом.\n"} {
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	data := `{"text_refs":{"external":["` + external + `","` + other + `"]}}`
	if err := os.WriteFile(filepath.Join(codex, "tools.json"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatalf("build corpus: %v", err)
	}
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, warning := range warnings {
		if strings.Contains(warning, "нет файла") {
			t.Errorf("reference to an external file reported missing: %v", warnings)
		}
	}
}

// A step that only another external file numbers is not a step of the corpus.
func TestExternalFilesDoNotProvideSteps(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	output := filepath.Join(root, "CLAUDE.md")
	external := filepath.Join(root, "agent.md")
	other := filepath.Join(root, "helper.md")
	externalRefsImport(t, codex, output, "CLAUDE.md", "# Бюджет\n")
	for path, body := range map[string]string{external: "Перед отгрузкой выполни шаг 7b.\n", other: "7b. Пересчитать ящики.\n"} {
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	data := `{"text_refs":{"external":["` + external + `","` + other + `"]}}`
	if err := os.WriteFile(filepath.Join(codex, "tools.json"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatalf("build corpus: %v", err)
	}
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	found := false
	for _, warning := range warnings {
		if strings.Contains(warning, external) && strings.Contains(warning, "нет шага 7b") {
			found = true
		}
	}
	if !found {
		t.Errorf("step numbered only in another external file should be missing: %v", warnings)
	}
}
