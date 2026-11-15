package lex

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNumberingIgnoresInterveningContent(t *testing.T) {
	for _, tc := range []struct {
		name, between string
	}{
		{"paragraph", "Поясняющий абзац.\n"},
		{"HTML comment", "<!-- пояснение -->\n"},
		{"table", "| столбец |\n| --- |\n| значение |\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "1. Первый пункт\n2. Второй пункт\n" + tc.between + "3. Третий пункт\n"
			findings := numberingFindings(filepath.Join(t.TempDir(), "guide.md"), body)
			if hasNumberingFinding(findings, "ожидался пункт") {
				t.Fatalf("продолжение списка после %s дало ложную находку: %v", tc.name, findings)
			}
		})
	}
}

func TestNumberingCountsMatchingSiblingSkillListLine(t *testing.T) {
	dir := t.TempDir()
	skill := filepath.Join(dir, "SKILL.md")
	commands := filepath.Join(dir, "commands.md")
	corpus := textCorpus{
		skill:    []byte("1. Первое действие\n2. Второе действие\n"),
		commands: []byte("1. Первое действие\n2. Второе действие\n"),
	}
	findings := numberingFindingsFromCorpus(corpus, commands)
	if hasNumberingFinding(findings, "ожидался пункт") {
		t.Fatalf("совпадающий пункт из SKILL.md пропустил номер списка: %v", findings)
	}
}

func TestNumberingStepCanNameSiblingFile(t *testing.T) {
	dir := t.TempDir()
	skill := filepath.Join(dir, "SKILL.md")
	commands := filepath.Join(dir, "commands.md")
	corpus := textCorpus{
		skill:    []byte("Выполни шаг 5 в `commands.md`.\n"),
		commands: []byte("1. Первый шаг\n2. Второй шаг\n3. Третий шаг\n4. Четвёртый шаг\n5. Пятый шаг\n"),
	}
	findings := numberingFindingsFromCorpus(corpus, skill)
	if hasNumberingFinding(findings, "нет шага 5") {
		t.Fatalf("шаг из явно названного соседнего файла не найден: %v", findings)
	}
}

func TestNumberingRecognizesStepHeadings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	findings := numberingFindings(path, "Выполни шаг 1.\n## Шаг 1\nОписание.\n")
	if hasNumberingFinding(findings, "нет шага 1") {
		t.Fatalf("шаг в заголовке не найден: %v", findings)
	}
}

func TestNumberingStillFindsRealGapsAndRestarts(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"missing step", "Выполни шаг 5.\n1. Первый шаг\n", "нет шага 5"},
		{"new list after paragraph", "1. Первый пункт\n2. Второй пункт\n\nЗатем другое:\n\n1. Новый список\n", ""},
		{"new list after code block", "1. Первый пункт\n2. Второй пункт\n\n```\nкод\n```\n\n1. Новый список\n", ""},
		{"jump after paragraph", "1. Первый пункт\n2. Второй пункт\n\nТекст.\n\n5. Пятый пункт\n", "ожидался пункт 3, найден 5"},
		{"list restarts after blank line", "1. Первый пункт\n2. Второй пункт\n\n1. Новый список\n", "ожидался пункт 3, найден 1"},
		{"numbered headings restart", "## 1. Первый\n## 2. Второй\n## 1. Снова первый\n", "ожидался раздел 3, найден 1"},
		{"list gap", "1. Первый пункт\n3. Третий пункт\n", "ожидался пункт 2, найден 3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := numberingFindings(filepath.Join(t.TempDir(), "guide.md"), tc.body)
			if tc.want == "" {
				if hasNumberingFinding(findings, "ожидался пункт") {
					t.Fatalf("неожиданная ошибка списка: %v", findings)
				}
				return
			}
			if !hasNumberingFinding(findings, tc.want) {
				t.Fatalf("не найдена ожидаемая ошибка %q: %v", tc.want, findings)
			}
		})
	}
}

func numberingFindings(path, body string) []textRefFinding {
	return numberingFindingsFromCorpus(textCorpus{path: []byte(body)}, path)
}

func numberingFindingsFromCorpus(corpus textCorpus, path string) []textRefFinding {
	findings := findTextRefs(corpus, filepath.Join(filepath.Dir(path), "decisions.md"))
	return withoutOrphanSkillFindings(findings)
}

func hasNumberingFinding(findings []textRefFinding, contains string) bool {
	for _, finding := range findings {
		if finding.kind == 4 && strings.Contains(finding.missing, contains) {
			return true
		}
	}
	return false
}
