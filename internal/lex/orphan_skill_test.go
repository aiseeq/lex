package lex

import (
	"path/filepath"
	"strings"
	"testing"
)

func onlyOrphanFindings(corpus textCorpus, journal string) []textRefFinding {
	var found []textRefFinding
	for _, finding := range findTextRefs(corpus, journal) {
		if finding.kind == 5 {
			found = append(found, finding)
		}
	}
	return found
}

func TestOrphanSkillFileWarns(t *testing.T) {
	root := t.TempDir()
	skill := filepath.Join(root, "skills", "sample")
	skillFile := filepath.Join(skill, "SKILL.md")
	a := filepath.Join(skill, "a.md")
	b := filepath.Join(skill, "b.md")
	journal := filepath.Join(root, "decisions.md")
	corpus := textCorpus{
		skillFile: []byte("# Инструкция\nСм. a.md\n"),
		a:         []byte("# А\n"),
		b:         []byte("\n# Б\nТекст\n"),
	}
	findings := onlyOrphanFindings(corpus, journal)
	if len(findings) != 1 || findings[0].path != b || findings[0].line != 2 || findings[0].text != "# Б" || !strings.Contains(findings[0].missing, "sample") {
		t.Fatalf("ожидалась только находка о b.md с заголовком и строкой 2: %v", findings)
	}
	corpus[skillFile] = []byte("# Инструкция\n")
	findings = onlyOrphanFindings(corpus, journal)
	if len(findings) != 2 {
		t.Fatalf("после удаления ссылки должны находиться a.md и b.md: %v", findings)
	}

	checks := []struct {
		name string
		add  func(textCorpus)
	}{
		{"outside", func(c textCorpus) { c[filepath.Join(root, "outside.md")] = []byte("a.md") }},
		{"self", func(c textCorpus) { c[a] = []byte("a.md") }},
		{"inside another name", func(c textCorpus) { c[skillFile] = []byte("# Инструкция\ndata.md\n") }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			c := textCorpus{skillFile: []byte("# Инструкция\n"), a: []byte("# А\n"), b: []byte("# Б\n")}
			check.add(c)
			found := onlyOrphanFindings(c, journal)
			if len(found) == 2 {
				for _, item := range found {
					if item.path == a {
						return
					}
				}
			}
			t.Errorf("упоминание не должно спасать a.md: %v", found)
		})
	}
}

func TestReferencedSkillFileDoesNotWarn(t *testing.T) {
	root := t.TempDir()
	skill := filepath.Join(root, "skills", "sample")
	skillFile := filepath.Join(skill, "SKILL.md")
	neighbor := filepath.Join(skill, "notes.md")
	a := filepath.Join(skill, "a.md")
	nested := filepath.Join(skill, "folder", "nested.md")
	journal := filepath.Join(root, "decisions.md")
	corpus := textCorpus{
		skillFile: []byte("# Инструкция\na.md notes.md\n"),
		neighbor:  []byte("folder/nested.md\n"),
		a:         []byte("# А\n"),
		nested:    []byte("# Вложенный\n"),
	}
	if findings := onlyOrphanFindings(corpus, journal); len(findings) != 0 {
		t.Fatalf("ссылки из SKILL.md и соседнего файла должны учитываться: %v", findings)
	}
	noSkill := filepath.Join(root, "skills", "without-instruction", "loose.md")
	if findings := onlyOrphanFindings(textCorpus{noSkill: []byte("# Без инструкции\n")}, journal); len(findings) != 0 {
		t.Fatalf("файл без SKILL.md в корпусе не относится к скиллу: %v", findings)
	}
}

func TestOrphanSkillFileExceptionApplies(t *testing.T) {
	root := t.TempDir()
	skillFile := filepath.Join(root, "skills", "sample", "SKILL.md")
	orphan := filepath.Join(root, "skills", "sample", "loose.md")
	corpus := textCorpus{skillFile: []byte("# Инструкция\n"), orphan: []byte("# Особый заголовок\n")}
	findings := findTextRefs(corpus, filepath.Join(root, "decisions.md"))
	exception := textRefException{Path: "skills/sample/loose.md", Kind: 5, Contains: "# Особый заголовок", Reason: "Сохранён намеренно."}
	filtered, unused := applyTextRefExceptions(corpus, findings, []textRefException{exception}, root)
	for _, finding := range filtered {
		if finding.kind == 5 && finding.path == orphan {
			t.Fatalf("исключение не погасило находку: %v", filtered)
		}
	}
	if len(unused) != 0 {
		t.Fatalf("исключение отмечено неиспользованным: %v", unused)
	}
}

func TestOrphanSkillFileBoundaries(t *testing.T) {
	root := t.TempDir()
	journal := filepath.Join(root, "decisions.md")
	skill := filepath.Join(root, "skills", "sample")
	skillFile := filepath.Join(skill, "SKILL.md")
	a := filepath.Join(skill, "a.md")
	lost := filepath.Join(skill, "folder", "lost.md")
	inner := filepath.Join(skill, "skills", "inner")
	innerFile := filepath.Join(inner, "x.md")
	notSkill := filepath.Join(root, "docs", "tool")
	corpus := textCorpus{
		skillFile:                            []byte("# Инструкция\nсм. pre-a.md и x.md\n"),
		a:                                    []byte("# Первый\n# Второй\n"),
		lost:                                 []byte("# Потерянный\n"),
		filepath.Join(inner, "SKILL.md"):     []byte("# Внутренний\n"),
		innerFile:                            []byte("# Икс\n"),
		filepath.Join(notSkill, "SKILL.md"):  []byte("# Не скилл\n"),
		filepath.Join(notSkill, "orphan.md"): []byte("# Сирота\n"),
	}
	byPath := map[string]textRefFinding{}
	for _, finding := range onlyOrphanFindings(corpus, journal) {
		byPath[finding.path] = finding
	}
	if finding, ok := byPath[a]; !ok || finding.line != 1 || finding.text != "# Первый" {
		t.Errorf("a.md после дефиса не упомянут, находка — по первому заголовку: %v", byPath)
	}
	if finding, ok := byPath[lost]; !ok || !strings.HasSuffix(finding.missing, " sample") {
		t.Errorf("файл в подпапке скилла без упоминаний — находка скилла sample: %v", byPath)
	}
	if finding, ok := byPath[innerFile]; !ok || !strings.HasSuffix(finding.missing, " inner") {
		t.Errorf("файл вложенного скилла принадлежит ему, упоминание во внешнем скилле не в счёт: %v", byPath)
	}
	if _, ok := byPath[filepath.Join(notSkill, "orphan.md")]; ok {
		t.Errorf("SKILL.md не под каталогом skills — не скилл: %v", byPath)
	}
	if len(byPath) != 3 {
		t.Errorf("ожидались ровно три находки: %v", byPath)
	}
}

func TestOrphanSkillFileNamesInsideLongerNames(t *testing.T) {
	root := t.TempDir()
	skill := filepath.Join(root, "skills", "sample")
	corpus := textCorpus{
		filepath.Join(skill, "SKILL.md"):    []byte("# Инструкция\nСм. a.md.bak, a.mdx и my notes.md\n"),
		filepath.Join(skill, "a.md"):        []byte("# А\n"),
		filepath.Join(skill, "a.md.bak"):    []byte("# Копия\n"),
		filepath.Join(skill, "a.mdx"):       []byte("# Икс\n"),
		filepath.Join(skill, "notes.md"):    []byte("# Заметки\n"),
		filepath.Join(skill, "my notes.md"): []byte("# Мои заметки\n"),
	}
	if findings := onlyOrphanFindings(corpus, filepath.Join(root, "decisions.md")); len(findings) != 0 {
		t.Fatalf("каждое имя, начатое с другого имени, упомянуто само и упоминает короткое: %v", findings)
	}
}

func TestOrphanSkillFileHeadingSkipsCodeBlock(t *testing.T) {
	root := t.TempDir()
	skill := filepath.Join(root, "skills", "sample")
	lost := filepath.Join(skill, "lost.md")
	corpus := textCorpus{
		filepath.Join(skill, "SKILL.md"): []byte("# Инструкция\n"),
		lost:                             []byte("```sh\n# install\n```\n# Потерянный\n"),
	}
	findings := onlyOrphanFindings(corpus, filepath.Join(root, "decisions.md"))
	if len(findings) != 1 || findings[0].line != 4 || findings[0].text != "# Потерянный" {
		t.Fatalf("строка находки — первый заголовок вне блока кода: %v", findings)
	}
}
