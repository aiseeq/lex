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
		source: []byte("Ссылка: раздел «Сроки и вёдра» в `x.md`.\n"),
		target: []byte("Сроки и вёдра\n"),
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

// ~/ is the home directory, not the directory of the file that holds the reference.
func TestHomeRefResolvesFromHomeNotFromFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	corpus := textCorpus{
		filepath.Join(home, "barn.md"):         []byte("# Солома\n"),
		filepath.Join(home, "yard", "barn.md"): []byte("# Сено\n"),
		filepath.Join(home, "yard", "note.md"): []byte("Ссылка: раздел «Солома» в `~/barn.md`.\n"),
	}
	if findings := findTextRefs(corpus, filepath.Join(home, "decisions.md")); len(findings) != 0 {
		t.Errorf("~/barn.md should resolve to the home directory, where the section is: %v", findings)
	}
}

// externalCheck builds a codex with CLAUDE.md and a decision journal, writes the
// external files and lists them in tools.json, and returns the check warnings.
func externalCheck(t *testing.T, files map[string]string, listed ...string) (string, []string, error) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	codex := filepath.Join(root, "codex")
	externalRefsImport(t, codex, filepath.Join(root, "CLAUDE.md"), "CLAUDE.md", "# Бюджет\n")
	externalRefsImport(t, codex, filepath.Join(root, "decisions.md"), "decisions.md", "# Решения\n")
	for name, body := range files {
		if strings.HasSuffix(name, "/") {
			if err := os.Mkdir(filepath.Join(root, name), 0755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	quoted := make([]string, 0, len(listed))
	for _, name := range listed {
		quoted = append(quoted, `"`+filepath.Join(root, name)+`"`)
	}
	data := `{"text_refs":{"external":[` + strings.Join(quoted, ",") + `]}}`
	if err := os.WriteFile(filepath.Join(codex, "tools.json"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatalf("build corpus: %v", err)
	}
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	return root, warnings, err
}

// A missing decision entry is an error in the corpus but only a warning in an external file.
func TestExternalMissingDecisionIsWarning(t *testing.T) {
	root, warnings, err := externalCheck(t, map[string]string{"agent.md": "- По decisions.md: no-such-crate-entry.\n"}, "agent.md")
	if err != nil {
		t.Fatalf("a missing decision entry in an external file failed check: %v", err)
	}
	if countWarningsContaining(warnings, filepath.Join(root, "agent.md")+":1: kind 1:") != 1 {
		t.Errorf("expected one kind 1 warning for the external file: %v", warnings)
	}
}

// Listing a corpus file in text_refs.external keeps it a corpus file: its kind 1 stays an error, printed once.
func TestExternalListingOfCorpusFileKeepsItsErrors(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	codex := filepath.Join(root, "codex")
	output := filepath.Join(root, "CLAUDE.md")
	externalRefsImport(t, codex, output, "CLAUDE.md", "# Бюджет\n\n- По decisions.md: no-such-crate-entry.\n")
	externalRefsImport(t, codex, filepath.Join(root, "decisions.md"), "decisions.md", "# Решения\n")
	externalRefsTools(t, codex, output)
	if err := Build(codex, false); err != nil {
		t.Fatalf("build corpus: %v", err)
	}
	_, err := CheckWithWarnings(codex, CheckOptions{})
	if err == nil || strings.Count(err.Error(), ": kind 1: ") != 1 {
		t.Errorf("corpus file listed as external should keep one kind 1 error: %v", err)
	}
}

// An external path that is a directory is a warning, said once; the other external files are still checked.
func TestUnreadableExternalFileIsWarning(t *testing.T) {
	root, warnings, err := externalCheck(t, map[string]string{"shed/": "", "agent.md": "Ссылка: раздел «Деньги» в `CLAUDE.md`.\n"}, "shed", "shed", "agent.md")
	if err != nil {
		t.Fatalf("a directory in text_refs.external failed check: %v", err)
	}
	shed := filepath.Join(root, "shed")
	if countWarningsContaining(warnings, shed+": external file for text references not read: is a directory") != 1 {
		t.Errorf("expected one warning naming the directory: %v", warnings)
	}
	if countWarningsContaining(warnings, filepath.Join(root, "agent.md")+":1: kind 2:") != 1 {
		t.Errorf("the other external file should still be checked: %v", warnings)
	}
}

// A line of a code block equal to the title is not a section.
func TestPlainLineInCodeBlockIsNotSection(t *testing.T) {
	dir := t.TempDir()
	corpus := textCorpus{
		filepath.Join(dir, "source.md"): []byte("Ссылка: раздел «Носорог» в `x.md`.\n"),
		filepath.Join(dir, "x.md"):      []byte("# Звери\n\n```\nНосорог\n```\n"),
	}
	findings := findTextRefs(corpus, filepath.Join(dir, "decisions.md"))
	if len(findings) != 1 || findings[0].kind != 2 {
		t.Errorf("a code block line should not count as the section: %v", findings)
	}
}
