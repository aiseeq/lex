package lex

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTextRefsKinds(t *testing.T) {
	dir := t.TempDir()
	journal := filepath.Join(dir, "decisions.md")
	source := filepath.Join(dir, "source.md")
	corpus := textCorpus{
		journal: []byte("## known-decision\n"),
		source:  []byte("decisions.md absent-decision\nраздел «Нет»\n`missing.md` рядом\n1. Первый\n3. Третий\n"),
	}
	findings := findTextRefs(corpus, journal)
	seen := map[int]bool{}
	for _, f := range findings {
		seen[f.kind] = true
	}
	for _, kind := range []int{1, 2, 3, 4} {
		if !seen[kind] {
			t.Errorf("класс %d не найден: %v", kind, findings)
		}
	}
}

func TestTextRefsResolved(t *testing.T) {
	dir := t.TempDir()
	journal := filepath.Join(dir, "decisions.md")
	source := filepath.Join(dir, "source.md")
	other := filepath.Join(dir, "other.md")
	corpus := textCorpus{
		journal: []byte("## known-decision\n"),
		source:  []byte("decisions.md known-decision\nраздел «Есть»\n`other.md` рядом\n## Есть\n1. Первый\n2. Второй\n"),
		other:   []byte("# Пример\n"),
	}
	if findings := findTextRefs(corpus, journal); len(findings) != 0 {
		t.Fatalf("исправные ссылки: %v", findings)
	}
}

func TestTextRefsDecisionList(t *testing.T) {
	dir := t.TempDir()
	journal := filepath.Join(dir, "decisions.md")
	source := filepath.Join(dir, "source.md")
	corpus := textCorpus{journal: []byte("## first-decision\n"), source: []byte("decisions.md first-decision, second-decision\n")}
	findings := findTextRefs(corpus, journal)
	if len(findings) != 1 || !strings.Contains(findings[0].missing, "second-decision") {
		t.Fatalf("ожидалась отсутствующая вторая запись: %v", findings)
	}
}

func TestTextRefsStagedRemovedDecision(t *testing.T) {
	repo := t.TempDir()
	codex := filepath.Join(repo, "codex")
	journal := filepath.Join(repo, "decisions.md")
	source := filepath.Join(repo, "source.md")
	for path, body := range map[string]string{journal: "## kept-decision\n\nТекст.\n", source: "См. decisions.md kept-decision.\n"} {
		if err := importData(codex, path, filepath.Base(path), []byte(body), false); err != nil {
			t.Fatal(err)
		}
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	git("init", "-q")
	git("add", ".")
	git("commit", "-q", "-m", "initial")
	frags, err := filepath.Glob(filepath.Join(codex, "frag", "decisions.md", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	frag := ""
	for _, path := range frags {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "## kept-decision\n") {
			frag = path
			mustWrite(t, frag, []byte(strings.Replace(string(raw), "## kept-decision\n", "", 1)))
		}
	}
	if frag == "" {
		t.Fatal("фрагмент заголовка не найден")
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	git("add", frag, journal)
	err = checkIn(t, repo, codex)
	if err == nil || !strings.Contains(err.Error(), "source.md:1: kind 1") || !strings.Contains(err.Error(), "kept-decision") {
		t.Fatalf("ожидалась ошибка ссылки из неизменённого файла: %v", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatal(err)
	}
}

func TestTextRefsForms(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".claude")
	journal := filepath.Join(root, "decisions.md")
	source := filepath.Join(root, "skills", "source", "SKILL.md")
	target := filepath.Join(root, "skills", "target", "SKILL.md")
	forms := []struct {
		name, line string
		corpus     textCorpus
		kind       int
	}{
		{"slug without decisions", "Правило `known-decision`.", textCorpus{journal: []byte("## known-decision\n"), source: []byte("Правило `known-decision`.\n")}, 1},
		{"section above", "См. раздел выше.", textCorpus{source: []byte("## Нужный раздел\nСм. раздел выше.\n")}, 2},
		{"section below", "См. раздел ниже.", textCorpus{source: []byte("См. раздел ниже.\n## Нужный раздел\n")}, 2},
		{"skill section", "(`target`, «Нужный раздел»)", textCorpus{source: []byte("(`target`, «Нужный раздел»)\n"), target: []byte("## Нужный раздел\n")}, 2},
		{"register", "Регистр «клиенты»", textCorpus{source: []byte("Регистр «клиенты»\nКлиенты — люди.\n")}, 2},
		{"step range", "Шаги 1–4 и 6 по плану.", textCorpus{source: []byte("Шаги 1–4 и 6 по плану.\n1. Один\n2. Два\n3. Три\n4. Четыре\n6. Шесть\n")}, 4},
	}
	for _, form := range forms {
		t.Run(form.name, func(t *testing.T) {
			corpus := form.corpus
			if form.name == "skill section" {
				corpus[source] = []byte(form.line + "\n")
			} else if form.name == "step range" {
				corpus[source] = []byte(form.line + "\n1. Один\n2. Два\n3. Три\n4. Четыре\n5. Пять\n6. Шесть\n")
			}
			findings := findTextRefs(corpus, journal)
			for _, f := range findings {
				if f.kind == form.kind && strings.Contains(f.text, form.line) {
					t.Fatalf("исправная форма дала находку: %v", f)
				}
			}
		})
	}

	broken := textCorpus{
		journal: []byte("## known-decision\n"),
		source:  []byte("Решение `missing-decision`.\nСм. раздел выше.\nСм. раздел ниже.\nСторож северного склада - Лея, ниже.\n(`target`, «Нет такого» )\nРегистр «несуществующие»\nШаги 1–4 и 6 по плану.\n1. Один\n2. Два\n4. Четыре\n"),
		target:  []byte("## Другое\n"),
	}
	findings := findTextRefs(broken, journal)
	for _, line := range []string{"missing-decision", "раздел выше", "раздел ниже", "Лея, ниже", "Нет такого", "несуществующие", "шага 3", "шага 6"} {
		found := false
		for _, f := range findings {
			if strings.Contains(strings.ToLower(f.missing+" "+f.text), strings.ToLower(line)) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("не найдена битая форма %q: %v", line, findings)
		}
	}
	namedBelow := filepath.Join(root, "skills", "sample", "addressees.md")
	withFullName := findTextRefs(textCorpus{namedBelow: []byte("Фамилия, ниже\n- Имя Фамилия (учётная запись)\n")}, journal)
	if len(withFullName) != 0 {
		t.Errorf("фамилия не сопоставилась с полным именем в пункте ниже: %v", withFullName)
	}
	withoutFullName := findTextRefs(textCorpus{namedBelow: []byte("Фамилия, ниже\n- Другое имя (учётная запись)\n")}, journal)
	if len(withoutFullName) != 1 || withoutFullName[0].kind != 2 {
		t.Errorf("отсутствующий пункт ниже не дал warning: %v", withoutFullName)
	}

	slugJournal := filepath.Join(t.TempDir(), "decisions.md")
	slugCases := []string{
		"по `sample-entry-one` (25.03)",
		"перечень — по `sample-entry-one`",
		"«... (пометкой на ящике, `sample-entry-two`) ...»",
		"(`sample-entry-three`)",
	}
	slugKinds := []int{1, 1, 2, 2}
	slugFindings := findTextRefs(textCorpus{slugJournal: []byte("## another-decision\n" + strings.Join(slugCases, "\n") + "\n")}, slugJournal)
	if len(slugFindings) != len(slugCases) {
		t.Fatalf("backSlug не нашёл каждую форму: %v", slugFindings)
	}
	for i, finding := range slugFindings {
		if finding.kind != slugKinds[i] || !strings.Contains(finding.text, slugCases[i]) {
			t.Errorf("ссылка %q дала kind %d, ожидался %d: %+v", slugCases[i], finding.kind, slugKinds[i], finding)
		}
	}
	registerSource := filepath.Join(root, "skills", "sample", "glossary.md")
	registerTarget := filepath.Join(root, "skills", "sample", "SKILL.md")
	registerFindings := findTextRefs(textCorpus{
		registerSource: []byte("Регистр - «начальство» в `SKILL.md`\n"),
		registerTarget: []byte("# Другой раздел\n"),
	}, journal)
	registerFindings = withoutOrphanSkillFindings(registerFindings)
	if len(registerFindings) != 1 || registerFindings[0].kind != 2 {
		t.Errorf("битый регистр в соседнем SKILL.md не найден: %v", registerFindings)
	}
	registerForm := filepath.Join(root, "skills", "sample", "addressees.md")
	registerSkill := filepath.Join(root, "skills", "sample", "SKILL.md")
	goodRegister := findTextRefs(textCorpus{
		registerForm:  []byte("Регистр из раздела «Температура погреба» для хранения корнеплодов зимой\n"),
		registerSkill: []byte("## Температура погреба\n"),
	}, journal)
	if goodRegister = withoutOrphanSkillFindings(goodRegister); len(goodRegister) != 0 {
		t.Errorf("ссылка «Регистр из раздела» с существующим заголовком дала находки: %v", goodRegister)
	}
	badRegister := withoutOrphanSkillFindings(findTextRefs(textCorpus{
		registerForm:  []byte("Регистр из раздела «Нет такого»\n"),
		registerSkill: []byte("## Температура погреба\n"),
	}, journal))
	if len(badRegister) != 1 || badRegister[0].kind != 2 {
		t.Errorf("битая форма «Регистр из раздела» не найдена: %v", badRegister)
	}
	interfaceLines := "В списке таких записей нет: они видны в журнале раздела «Сводка урожая» и в отчёте.\nКнопка на экране раздела «Полив».\nОтвет Telegram-бота раздела «Теплицы».\n"
	documentLines := "См. раздела «Нет такого раздела» в начале.\nТаблица в приложении раздела «Нет приложения».\nНа странице раздела «Нет страницы».\nЖурналист раздела «Нет журналиста» пишет.\nБотаника раздела «Нет ботаники».\n"
	interfacePath := filepath.Join(root, "rules", "greenhouse.md")
	interfaceFindings := findTextRefs(textCorpus{interfacePath: []byte("## Теплица\n" + interfaceLines + documentLines)}, journal)
	for _, missing := range []string{"Нет такого раздела", "Нет приложения", "Нет страницы", "Нет журналиста", "Нет ботаники"} {
		found := false
		for _, f := range interfaceFindings {
			found = found || f.kind == 2 && strings.Contains(f.text, missing)
		}
		if !found {
			t.Errorf("ссылка на раздел документа «%s» не найдена: %v", missing, interfaceFindings)
		}
	}
	if len(interfaceFindings) != 5 {
		t.Errorf("раздел интерфейса после «журнале», «экране» или «бота» не должен быть ссылкой: %v", interfaceFindings)
	}
	removedRule := "Снимает в `sample-entry-one` и поправке 2 `sample-entry-two` правило «после выкопки сушить гладиолусы под навесом, затем убрать плетёные корзины в сухой погреб до первых заморозков»"
	decisionPath := filepath.Join(root, "decisions.md")
	quotedCorpus := removedRule + "\nПравило «несуществующее»\nПравила «историческая цитата»\nПравило «снятое» снято 25.04\n"
	quotedFindings := findTextRefs(textCorpus{decisionPath: []byte(quotedCorpus)}, decisionPath)
	if len(quotedFindings) != 1 || !strings.Contains(quotedFindings[0].text, "несуществующее") {
		t.Errorf("снятое историческое правило не должно быть ложной ссылкой, обычное битое правило должно находиться: %v", quotedFindings)
	}
}

func TestTextRefsExceptions(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".claude")
	chat := filepath.Join(root, "skills", "sample", "SKILL.md")
	other := filepath.Join(root, "skills", "other", "SKILL.md")
	settings, err := (&corpusReader{}).textRefSettings(filepath.Join("testdata", "text-refs-codex"))
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.TextRefs.Exceptions) != 19 {
		t.Fatalf("в учебном кодексе должно быть 19 исключений, получено %d", len(settings.TextRefs.Exceptions))
	}
	fixtureCorpus := textCorpus{}
	fixtureFindings := make([]textRefFinding, 0, len(settings.TextRefs.Exceptions))
	fixtureLines := map[string][]string{}
	for i, configured := range settings.TextRefs.Exceptions {
		path := filepath.Join(root, filepath.FromSlash(configured.Path))
		fixtureLines[path] = append(fixtureLines[path], configured.Contains)
		fixtureFindings = append(fixtureFindings, textRefFinding{kind: configured.Kind, path: path, text: configured.Contains})
		if configured.Reason == "" {
			t.Errorf("исключение %d без причины", i)
		}
	}
	for path, lines := range fixtureLines {
		fixtureCorpus[path] = []byte(strings.Join(lines, "\n"))
	}
	fixtureFiltered, fixtureUnused := applyTextRefExceptions(fixtureCorpus, fixtureFindings, settings.TextRefs.Exceptions, root)
	if len(fixtureFiltered) != 0 || len(fixtureUnused) != 0 {
		t.Fatalf("исключения учебного кодекса не сработали: остаток %v, мёртвые %v", fixtureFiltered, fixtureUnused)
	}
	exception := settings.TextRefs.Exceptions[2]
	body := []byte(strings.Repeat("\n", 18) + exception.Contains + "\n")
	corpus := textCorpus{chat: body, other: body}
	findings := []textRefFinding{
		{kind: exception.Kind, path: chat, line: 19, text: exception.Contains},
		{kind: exception.Kind, path: other, line: 19, text: exception.Contains},
	}
	filtered, unused := applyTextRefExceptions(corpus, findings, []textRefException{exception}, root)
	chatFinding, otherFinding := false, false
	for _, finding := range filtered {
		if finding.kind == exception.Kind && finding.path == chat {
			chatFinding = true
		}
		if finding.kind == exception.Kind && finding.path == other {
			otherFinding = true
		}
	}
	if chatFinding {
		t.Fatal("исключение не погасило свою находку")
	}
	if !otherFinding {
		t.Fatal("исключение погасило такую же находку в другом файле")
	}
	if len(unused) != 0 {
		t.Fatalf("сработавшее исключение помечено неиспользованным: %v", unused)
	}

	shifted := textCorpus{chat: append([]byte("заголовок перечня\n"), body...), other: body}
	shiftedFindings := []textRefFinding{
		{kind: exception.Kind, path: chat, line: 20, text: exception.Contains},
		{kind: exception.Kind, path: other, line: 19, text: exception.Contains},
	}
	shiftedFiltered, shiftedUnused := applyTextRefExceptions(shifted, shiftedFindings, []textRefException{exception}, root)
	if len(shiftedUnused) != 0 {
		t.Fatalf("исключение не пережило сдвиг строки: %v", shiftedUnused)
	}
	chatFinding = false
	for _, finding := range shiftedFiltered {
		if finding.path == chat && finding.kind == exception.Kind {
			chatFinding = true
		}
	}
	if chatFinding {
		t.Fatal("после сдвига строк исключение перестало гасить свою находку")
	}

	dead := textRefException{Path: "skills/sample/SKILL.md", Kind: 4, Contains: "нет такого фрагмента", Reason: "Проверка мёртвого исключения."}
	_, unused = applyTextRefExceptions(corpus, findings, []textRefException{dead}, root)
	if len(unused) != 1 || !strings.Contains(unused[0], exceptionKey(dead)) {
		t.Fatalf("неиспользованное исключение должно выдать warning с ключом: %v", unused)
	}

	otherKind := []textRefFinding{{kind: exception.Kind%4 + 1, path: chat, line: 19, text: exception.Contains}}
	kept, unused := applyTextRefExceptions(corpus, otherKind, []textRefException{exception}, root)
	if len(kept) != 1 || len(unused) != 1 {
		t.Fatalf("исключение погасило находку другого вида: остаток %v, мёртвые %v", kept, unused)
	}

	twice := textCorpus{chat: append(append([]byte(nil), body...), body...), other: body}
	kept, unused = applyTextRefExceptions(twice, findings[:1], []textRefException{exception}, root)
	if len(kept) != 1 || len(unused) != 1 {
		t.Fatalf("подстрока исключения встречается в файле дважды — исключение не должно гасить: остаток %v, мёртвые %v", kept, unused)
	}
}

func TestCheckOutputsUnderKnowsCodexSkills(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	skill := filepath.Join(root, "skills", "sample-process", "SKILL.md")
	project := filepath.Join(root, "projects", "garden", "CLAUDE.md")
	mustWrite(t, filepath.Join(root, "decisions.md"), []byte("## other-decision\n- Решение.\n"))
	if err := importData(codex, skill, "skill.md", []byte("# Процесс\n- Правило.\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := importData(codex, project, "garden.md", []byte("# Кладовая\n- Взвесить тележку `cart` по `sample-process`.\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	for _, under := range []string{root, filepath.Dir(project)} {
		warnings, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: under})
		if err != nil {
			t.Fatalf("outputs under %s: a skill named after «по» was read as a decision: %v", under, err)
		}
		for _, warning := range warnings {
			if strings.Contains(warning, "sample-process") {
				t.Fatalf("outputs under %s: unexpected warning %q", under, warning)
			}
		}
	}
	if err := importData(codex, filepath.Join(root, "projects", "other", "CLAUDE.md"), "other.md", []byte("# Other\n- По `no-such-skill`.\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: filepath.Join(root, "projects", "other")}); err == nil || !strings.Contains(err.Error(), "нет записи no-such-skill") {
		t.Fatalf("an unknown name after «по» must still need a decision: %v", err)
	}
}

// withoutOrphanSkillFindings drops kind 5 findings for tests whose sample skill files are not about skill coverage.
func withoutOrphanSkillFindings(findings []textRefFinding) []textRefFinding {
	var kept []textRefFinding
	for _, finding := range findings {
		if finding.kind != 5 {
			kept = append(kept, finding)
		}
	}
	return kept
}
