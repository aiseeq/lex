package lex

import (
	"os"
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

func TestNumberingInterruptionRequiresUnindentedText(t *testing.T) {
	for _, tc := range []struct {
		name, between string
	}{
		{"indented prose", "   Пояснение.\n"},
		{"nested bullet", "   - вложенный пункт\n"},
		{"indented code fence", "   ```\n   код\n   ```\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "1. a\n" + tc.between + "1. b\n"
			findings := numberingFindings(filepath.Join(t.TempDir(), "guide.md"), body)
			if !hasNumberingFinding(findings, "ожидался пункт 2, найден 1") {
				t.Fatalf("отступный текст не должен прерывать список: %v", findings)
			}
		})
	}
}

func TestNumberingRestartAfterTextAcceptsZero(t *testing.T) {
	body := "1. a\n2. b\n\nТекст.\n\n0. c\n1. d\n"
	findings := numberingFindings(filepath.Join(t.TempDir(), "guide.md"), body)
	if hasNumberingFinding(findings, "ожидался пункт") {
		t.Fatalf("после текста новый список может начинаться с нуля: %v", findings)
	}
}

func TestNumberingStepUsesNearestNamedFile(t *testing.T) {
	dir := t.TempDir()
	skill := filepath.Join(dir, "SKILL.md")
	a := filepath.Join(dir, "a.md")
	b := filepath.Join(dir, "b.md")
	corpus := textCorpus{
		skill: []byte("Шаг 5 в `a.md`, шаг 2 в `b.md`.\n"),
		a:     []byte("1. a\n2. b\n"),
		b:     []byte("1. a\n2. b\n3. c\n4. d\n5. e\n"),
	}
	findings := numberingFindingsFromCorpus(corpus, skill)
	if !hasNumberingFinding(findings, "нет шага 5") {
		t.Fatalf("шаг 5 должен проверяться в ближайшем файле a.md: %v", findings)
	}
}

func TestNumberingNamedFileControlsStepLookup(t *testing.T) {
	t.Run("missing file is not checked", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "SKILL.md")
		findings := numberingFindings(path, "Шаг 5 в `other.md`.\n")
		if hasNumberingFinding(findings, "нет шага 5") {
			t.Fatalf("отсутствующий файл не должен откатываться на локальные шаги: %v", findings)
		}
	})

	t.Run("step absent in the named file and the own file", func(t *testing.T) {
		dir := t.TempDir()
		skill := filepath.Join(dir, "SKILL.md")
		commands := filepath.Join(dir, "commands.md")
		corpus := textCorpus{
			skill:    []byte("1. a\n2. b\nШаг 5 в `commands.md`.\n"),
			commands: []byte("1. a\n2. b\n3. c\n"),
		}
		findings := numberingFindingsFromCorpus(corpus, skill)
		if !hasNumberingFinding(findings, "нет шага 5") {
			t.Fatalf("шага нет ни в commands.md, ни в своём файле: %v", findings)
		}
	})

	t.Run("own file step with an output file named after it", func(t *testing.T) {
		dir := t.TempDir()
		skill := filepath.Join(dir, "SKILL.md")
		report := filepath.Join(dir, "report.md")
		corpus := textCorpus{
			skill:  []byte("1. a\n2. b\n3. c\nНа шаге 3 в `report.md` появится раздел.\n"),
			report: []byte("1. Итог\n"),
		}
		if findings := numberingFindingsFromCorpus(corpus, skill); len(findings) > 0 {
			t.Fatalf("шаг своего файла с выходным файлом после него: %v", findings)
		}
	})
}

func TestNumberingStepSuffixIsCaseInsensitive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	findings := numberingFindings(path, "## ШАГ 2A\n\nСм. шаг 2a.\n")
	if hasNumberingFinding(findings, "нет шага") {
		t.Fatalf("буквенный суффикс шага должен сравниваться без учёта регистра: %v", findings)
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

func TestNumberingSkipsIndentedCodeInsideItem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guide.md")
	findings := numberingFindings(path, "1. a\n   ```\n   3. x\n   шаг 9\n   ```\n2. b\n")
	if len(findings) > 0 {
		t.Fatalf("код с отступом внутри пункта проверен как текст: %v", findings)
	}
}

func TestNumberingBindsFileOnlyRightAfterStep(t *testing.T) {
	commands := "1. a\n2. b\n3. c\n4. d\n5. e\n"
	for _, tc := range []struct{ name, line string }{
		{"в файле", "Шаг 5 в файле `commands.md`.\n"},
		{"из", "Шаг 5 из `commands.md`.\n"},
		{"markdown link", "Шаг 5 в [commands](commands.md).\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			corpus := textCorpus{
				filepath.Join(dir, "SKILL.md"):    []byte("1. Один\n" + tc.line),
				filepath.Join(dir, "commands.md"): []byte(commands),
			}
			if findings := numberingFindingsFromCorpus(corpus, filepath.Join(dir, "SKILL.md")); len(findings) > 0 {
				t.Fatalf("шаг из названного файла не найден: %v", findings)
			}
		})
	}
	dir := t.TempDir()
	corpus := textCorpus{
		filepath.Join(dir, "SKILL.md"): []byte("1. a\n2. b\nПосле шага 2 запиши итог в `notes.md`.\n"),
		filepath.Join(dir, "notes.md"): []byte("Заметки.\n"),
	}
	if findings := numberingFindingsFromCorpus(corpus, filepath.Join(dir, "SKILL.md")); len(findings) > 0 {
		t.Fatalf("файл назначения принят за место шага: %v", findings)
	}
}

func TestNumberingNamedFileWithoutStepsIsStepOutput(t *testing.T) {
	dir := t.TempDir()
	corpus := textCorpus{
		filepath.Join(dir, "SKILL.md"):  []byte("1. a\n2. b\n3. c\nНа шаге 3 в `report.md` появится раздел.\n"),
		filepath.Join(dir, "report.md"): []byte("# Отчёт\n\nТекст.\n"),
	}
	if findings := numberingFindingsFromCorpus(corpus, filepath.Join(dir, "SKILL.md")); len(findings) > 0 {
		t.Fatalf("файл без шагов принят за место шага: %v", findings)
	}
}

// A paragraph that puts steps 1–4 in another file's section counts them: the
// list of this file goes on from step 5 and the steps are not missing.
func TestNumberingStepsInAnotherFileSection(t *testing.T) {
	dir := t.TempDir()
	skill := filepath.Join(dir, "SKILL.md")
	guide := filepath.Join(dir, "guide.md")
	outside := filepath.Join(t.TempDir(), "CLAUDE.md")
	if err := os.WriteFile(outside, []byte("Вступление.\n\nПриёмка\n1. a\n2. b\n3. c\n4. d\n"), 0644); err != nil {
		t.Fatal(err)
	}
	body := "Шаги 1–4 делает мастер по `%s`, «Приёмка»; шаг 5 — свой.\n  Зачем: пояснение\n\n5. Пятый шаг\n"
	cases := []struct {
		name, ref string
		corpus    textCorpus
	}{
		{"file outside the corpus, plain heading", outside, textCorpus{}},
		{"file in the corpus with the section", "guide.md", textCorpus{guide: []byte("## Приёмка\n1. a\n2. b\n3. c\n4. d\n## Другое\n")}},
		{"list after the reference already started", "guide.md", textCorpus{guide: []byte("## Приёмка\n1. a\n2. b\n3. c\n4. d\n")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := strings.Replace(body, "%s", tc.ref, 1)
			if tc.name == "list after the reference already started" {
				text = "Шаги 1–4 по `guide.md`, «Приёмка».\n\n5. Пятый шаг\n6. Шестой шаг\n"
			}
			tc.corpus[skill] = []byte(text)
			findings := numberingFindingsFromCorpus(tc.corpus, skill)
			if hasNumberingFinding(findings, "нет шага") || hasNumberingFinding(findings, "ожидался пункт") {
				t.Fatalf("шаги из раздела другого файла не засчитаны: %v", findings)
			}
		})
	}
}

func TestNumberingStepsElsewhereStillNeedFileSectionAndSteps(t *testing.T) {
	dir := t.TempDir()
	skill := filepath.Join(dir, "SKILL.md")
	guide := filepath.Join(dir, "guide.md")
	outside := filepath.Join(t.TempDir(), "CLAUDE.md")
	if err := os.WriteFile(outside, []byte("## Приёмка\n1. a\n2. b\n3. c\n4. d\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ref := "Шаги 1–4 по `guide.md`, «Приёмка».\n\n"
	full := "## Приёмка\n1. a\n2. b\n3. c\n4. d\n"
	cases := []struct {
		name, body, guide, want string
	}{
		{"no section", "Шаги 1–4 делает мастер по `" + outside + "`.\n\n5. Пятый шаг\n", "", "ожидался пункт 1, найден 5"},
		{"no file", "Шаги 1–4 делает мастер, «Приёмка».\n\n5. Пятый шаг\n", "", "нет шага 1"},
		{"section after the clause", "Шаги 1–4 делает мастер; см. `" + outside + "`, «Приёмка».\n\n5. Пятый шаг\n", "", "нет шага 2"},
		{"section missing in the corpus file", ref + "5. Пятый шаг\n", "## Другое\n1. a\n2. b\n3. c\n4. d\n", "нет шага 3"},
		{"step missing in the corpus file", ref + "5. Пятый шаг\n", "## Приёмка\n1. a\n2. b\n3. c\n", "нет шага 4"},
		{"list skips past the counted steps", "Шаги 1–2 по `" + outside + "`, «Приёмка».\n\n4. Четвёртый шаг\n", "", "ожидался пункт 1, найден 4"},
		{"steps in another section of the file", ref + "5. Пятый шаг\n", "## Приёмка\nТекст.\n## Другое\n1. a\n2. b\n3. c\n4. d\n", "нет шага 1"},
		{"a bullet that mentions the title is not the section", ref + "5. Пятый шаг\n", "- Приёмка описана где-то ещё\n1. a\n2. b\n3. c\n4. d\n", "нет шага 1"},
		{"counted steps do not carry to the next section", ref + "5. Пятый шаг\n## Другой раздел\n5. Тоже пятый\n", full, "ожидался пункт 1, найден 5"},
		{"file and quote not after a preposition", "Шаги 1–4 описаны ниже, пример — `guide.md`, «Приёмка».\n\n5. Пятый шаг\n", full, "нет шага 1"},
		{"quote that is no section of the outside file", "Шаги 1–4 по `" + outside + "`, «как обычно».\n\n5. Пятый шаг\n", "", "нет шага 1"},
		{"local items overlap the counted steps", "Шаги 1–4 по `" + outside + "`, «Приёмка».\n\n1. a\n2. b\n5. c\n", "", "ожидался пункт 3, найден 5"},
		{"nested list does not take the counted steps", "Шаги 1–4 по `" + outside + "`, «Приёмка».\n\n1. a\n   5. подпункт\n", "", "ожидался пункт 1, найден 5"},
		{"misspelt sibling file", "Шаги 1–4 по `gide.md`, «Приёмка».\n\n5. Пятый шаг\n", full, "нет шага 1"},
		{"second list after the first one", "Шаги 1–4 по `" + outside + "`, «Приёмка».\n\n5. a\n\nТекст.\n\n5. b\n", "", "ожидался пункт 6, найден 5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			corpus := textCorpus{skill: []byte(tc.body)}
			if tc.guide != "" {
				corpus[guide] = []byte(tc.guide)
			}
			findings := numberingFindingsFromCorpus(corpus, skill)
			if !hasNumberingFinding(findings, tc.want) {
				t.Fatalf("не найдена ожидаемая находка %q: %v", tc.want, findings)
			}
		})
	}
}

// A steps file that is there but cannot be read is named, not taken for a missing one.
func TestNumberingStepsFileUnreadableIsReported(t *testing.T) {
	dir := t.TempDir()
	skill := filepath.Join(dir, "SKILL.md")
	unreadable := filepath.Join(t.TempDir(), "x.md")
	if err := os.Mkdir(unreadable, 0755); err != nil {
		t.Fatal(err)
	}
	corpus := textCorpus{skill: []byte("Шаги 1–4 по `" + unreadable + "`, «Приёмка».\n\n5. Пятый шаг\n")}
	findings := numberingFindingsFromCorpus(corpus, skill)
	if !hasNumberingFinding(findings, "не прочитан файл шагов "+unreadable) || !hasNumberingFinding(findings, "нет шага 1") {
		t.Fatalf("нечитаемый файл шагов не назван: %v", findings)
	}
}

// The section of a plain or bold line is one block, code blocks hold no steps
// and no headings, and the counted steps go to the list right after the reference.
func TestNumberingStepsElsewhereSectionBounds(t *testing.T) {
	dir := t.TempDir()
	skill := filepath.Join(dir, "SKILL.md")
	guide := filepath.Join(dir, "guide.md")
	ref := "Шаги 1–4 по `guide.md`, «Приёмка».\n\n"
	full := "## Приёмка\n1. a\n2. b\n3. c\n4. d\n"
	cases := []struct {
		name, body, guide, want string
	}{
		{"plain section stops at the next plain section", ref + "5. e\n", "Приёмка\nТекст без шагов.\n\nДругое\n1. a\n2. b\n3. c\n4. d\n", "нет шага 1"},
		{"bold section stops at the next bold section", ref + "5. e\n", "**Приёмка**\nТекст.\n\n**Другое**\n1. a\n2. b\n3. c\n4. d\n", "нет шага 1"},
		{"plain section stops at unindented text after its list", ref + "5. e\n", "Приёмка\n1. a\n2. b\nДругое\n3. c\n4. d\n", "нет шага 3"},
		{"steps only in a code block", ref + "5. e\n", "## Приёмка\n```\n1. a\n2. b\n3. c\n4. d\n```\n", "нет шага 1"},
		{"steps only as sub-items", ref + "5. e\n", "## Приёмка\n- пункт\n  1. a\n  2. b\n  3. c\n  4. d\n", "нет шага 1"},
		{"list starts at the last counted step", ref + "4. e\n5. f\n", full, "ожидался пункт 1, найден 4"},
		{"list starts inside the counted steps", ref + "2. e\n3. f\n4. g\n", full, "ожидался пункт 1, найден 2"},
		{"another bold section between", ref + "**Другой раздел**\nТекст.\n5. e\n", full, "ожидался пункт 1, найден 5"},
		{"another paragraph between", ref + "Другой текст.\n\n5. e\n", full, "ожидался пункт 1, найден 5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := numberingFindingsFromCorpus(textCorpus{skill: []byte(tc.body), guide: []byte(tc.guide)}, skill)
			if !hasNumberingFinding(findings, tc.want) {
				t.Fatalf("не найдена ожидаемая находка %q: %v", tc.want, findings)
			}
		})
	}
	for _, tc := range []struct{ name, guide string }{
		{"code block with a # line before the steps", "## Приёмка\n```\n# setup\n```\n1. a\n2. b\n3. c\n4. d\n"},
		{"plain section with a code block before the steps", "Приёмка\n```\n# setup\n```\n1. a\n2. b\n3. c\n4. d\n"},
		{"plain section with a lead-in and indented lines", "Приёмка\nВводная:\n\n1. a\n   пояснение\n  Зачем: так\n2. b\n3. c\n4. d\n\nДальше другое.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings := numberingFindingsFromCorpus(textCorpus{skill: []byte(ref + "5. e\n"), guide: []byte(tc.guide)}, skill)
			if hasNumberingFinding(findings, "нет шага") || hasNumberingFinding(findings, "ожидался пункт") {
				t.Fatalf("верная ссылка дала находку: %v", findings)
			}
		})
	}
}

// More forms of a reference to steps in another file's section.
func TestNumberingStepsElsewhereReferenceForms(t *testing.T) {
	dir := t.TempDir()
	skill := filepath.Join(dir, "SKILL.md")
	guide := filepath.Join(dir, "guide.md")
	full := "## Приёмка\n1. a\n2. b\n3. c\n4. d\n"
	for _, tc := range []struct{ name, ref, guide string }{
		{"section before the file", "Шаги 1–4 по разделу «Приёмка» в `guide.md`.", full},
		{"markdown link", "Шаги 1–4 по [гайду](guide.md), «Приёмка».", full},
		{"markdown link with an anchor", "Шаги 1–4 по [гайду](guide.md#priemka), «Приёмка».", full},
		{"section in parentheses", "Шаги 1–4 по `guide.md` (раздел «Приёмка»).", full},
		{"в разделе after the file", "Шаги 1–4 по `guide.md`, в разделе «Приёмка».", full},
		{"abbreviation in the clause", "Шаги 1–4 делает мастер, т. е. по `guide.md`, «Приёмка».", full},
		{"reference in a heading", "### Шаги 1–4 делает мастер по `guide.md`, «Приёмка»", full},
		{"numbered heading of the section", "Шаги 1–4 по `guide.md`, «Приёмка».", "## 3. Приёмка\n1. a\n2. b\n3. c\n4. d\n"},
		{"plain section with step headings", "Шаги 1–4 по `guide.md`, «Приёмка».", "Приёмка\n### Шаг 1\nОписание.\n\n### Шаг 2\nОписание.\n### Шаг 3\n### Шаг 4\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			corpus := textCorpus{skill: []byte(tc.ref + "\n\n5. e\n"), guide: []byte(tc.guide)}
			findings := numberingFindingsFromCorpus(corpus, skill)
			if hasNumberingFinding(findings, "нет шага") || hasNumberingFinding(findings, "ожидался пункт") {
				t.Fatalf("верная ссылка дала находку: %v", findings)
			}
		})
	}
	for _, tc := range []struct{ name, ref, guide, want string }{
		{"section first without a file", "Шаги 1–4 по разделу «Приёмка».", full, "нет шага 1"},
		{"sentence ends before the file", "Шаги 1–4 делает мастер. Пример — по `guide.md`, «Приёмка».", full, "нет шага 1"},
		{"plain section with step headings stops at another heading", "Шаги 1–4 по `guide.md`, «Приёмка».", "Приёмка\n### Шаг 1\n### Шаг 2\n## Другое\n### Шаг 3\n### Шаг 4\n", "нет шага 3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			corpus := textCorpus{skill: []byte(tc.ref + "\n\n5. e\n"), guide: []byte(tc.guide)}
			if findings := numberingFindingsFromCorpus(corpus, skill); !hasNumberingFinding(findings, tc.want) {
				t.Fatalf("не найдена ожидаемая находка %q: %v", tc.want, findings)
			}
		})
	}
}

// A sentence ends at a dot before a space whatever starts the next one, and a
// plain or bold section does not take numbered sections or a stray list.
func TestNumberingStepsElsewhereFormsStayNarrow(t *testing.T) {
	dir := t.TempDir()
	skill := filepath.Join(dir, "SKILL.md")
	guide := filepath.Join(dir, "guide.md")
	full := "## Приёмка\n1. a\n2. b\n3. c\n4. d\n"
	for _, tc := range []struct{ name, ref, guide, want string }{
		{"next sentence starts with code", "Шаги 1–4 делает мастер. `lex check` гоняй по `guide.md`, «Приёмка».", full, "нет шага 1"},
		{"next sentence starts with bold", "Шаги 1–4 делает мастер. **Боец** смотрит по `guide.md`, «Приёмка».", full, "нет шага 1"},
		{"next sentence starts with a quote", "Шаги 1–4 делает мастер. «Пример» лежит по `guide.md`, «Приёмка».", full, "нет шага 1"},
		{"next sentence after an exclamation", "Шаги 1–4 делает мастер! Пример по `guide.md`, «Приёмка».", full, "нет шага 1"},
		{"bold section before numbered sections", "Шаги 1–4 по `guide.md`, «Приёмка».", "**Приёмка**\n1. a\n2. b\n## 3. Деплой\n## 4. Итог\n", "нет шага 3"},
		{"plain section before a numbered section with a list", "Шаги 1–4 по `guide.md`, «Приёмка».", "Приёмка\n1. a\n2. b\n## 1. Деплой\n1. a\n2. b\n3. c\n4. d\n", "нет шага 3"},
		{"stray list after step headings", "Шаги 1–4 по `guide.md`, «Приёмка».", "Приёмка\n### Шаг 1\n### Шаг 2\nДругая тема.\n\n1. a\n2. b\n3. c\n4. d\n", "нет шага 3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			corpus := textCorpus{skill: []byte(tc.ref + "\n\n5. e\n"), guide: []byte(tc.guide)}
			if findings := numberingFindingsFromCorpus(corpus, skill); !hasNumberingFinding(findings, tc.want) {
				t.Fatalf("не найдена ожидаемая находка %q: %v", tc.want, findings)
			}
		})
	}
}
