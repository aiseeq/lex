package lex

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func quoteFixture(t *testing.T, source string) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	output := filepath.Join(root, "CLAUDE.md")
	if err := os.WriteFile(output, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, output, ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	return root, codex, output
}

func TestQuotesToBasis(t *testing.T) {
	quote := `(пользователь 03.02: «Укажи номер страницы»)`
	secondQuote := `(пользователь 05.02: «Оставь исходный порядок»)`
	root, codex, output := quoteFixture(t, "Правило полезного ответа "+quote+" и "+secondQuote+".\n")
	reportPath := filepath.Join(root, "dry.json")
	if err := QuotesToBasis(codex, QuotesToBasisOptions{Report: reportPath}); err != nil {
		t.Fatal(err)
	}
	if err := QuotesToBasis(codex, QuotesToBasisOptions{Apply: true, Report: reportPath}); err != nil {
		t.Fatal(err)
	}
	fragments, err := filepath.Glob(filepath.Join(codex, "frag", "*", "*.md"))
	if err != nil || len(fragments) != 1 {
		t.Fatalf("fragments = %v, %v", fragments, err)
	}
	fragment, err := os.ReadFile(fragments[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fragment), "основание: "+quote+"; "+secondQuote) || strings.Contains(string(fragment), "Правило полезного ответа "+quote) || strings.Contains(string(fragment), " и "+secondQuote) {
		t.Fatalf("fragment did not move exact dated quote into basis: %s", fragment)
	}
	if err := Build(codex, true); err != nil {
		t.Fatalf("build --check: %v", err)
	}
	if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
		t.Fatalf("check: %v", err)
	}
	built, err := os.ReadFile(output)
	if err != nil || strings.Contains(string(built), quote) {
		t.Fatalf("built file still has citation: %q, %v", built, err)
	}
	var report quotesReport
	reportBytes, err := os.ReadFile(reportPath)
	if err != nil || json.Unmarshal(reportBytes, &report) != nil || report.FragmentsMoved != 1 {
		t.Fatalf("dry-run report = %s, %v", reportBytes, err)
	}
}

func TestQuotesToBasisLeavesNewFormsInText(t *testing.T) {
	root, codex, output := quoteFixture(t, "Правило (пользователь 25.04 у теплицы «грядка за навесом»: «цитата»).\n")
	reportPath := filepath.Join(root, "apply.json")
	if err := QuotesToBasis(codex, QuotesToBasisOptions{Apply: true, Report: reportPath}); err != nil {
		t.Fatal(err)
	}
	fragments, err := filepath.Glob(filepath.Join(codex, "frag", "*", "*.md"))
	if err != nil || len(fragments) != 1 {
		t.Fatalf("fragments = %v, %v", fragments, err)
	}
	fragment, err := os.ReadFile(fragments[0])
	if err != nil || !strings.Contains(string(fragment), "основание: \n") || !strings.Contains(string(fragment), "пользователь 25.04 у") {
		t.Fatalf("new quote form was moved: fragment = %s, err = %v", fragment, err)
	}
	built, err := os.ReadFile(output)
	if err != nil || !strings.Contains(string(built), "пользователь 25.04 у") {
		t.Fatalf("output no longer contains new quote form: %s, err = %v", built, err)
	}
	var report quotesReport
	reportBytes, err := os.ReadFile(reportPath)
	if err != nil || json.Unmarshal(reportBytes, &report) != nil || report.FragmentsMoved != 0 {
		t.Fatalf("apply report = %s, %v", reportBytes, err)
	}
}

func TestQuotesForeignRefused(t *testing.T) {
	root, owner, out := quoteFixture(t, "Правило (пользователь 03.02: «слово»).\n")
	if err := setManifestHeader(owner, filepath.Base(out), true); err != nil {
		t.Fatal(err)
	}
	if err := Build(owner, false); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(root, "foreign")
	if err := os.CopyFS(foreign, os.DirFS(owner)); err != nil {
		t.Fatal(err)
	}
	beforeOut, _ := os.ReadFile(out)
	paths, _ := filepath.Glob(filepath.Join(foreign, "frag", "*", "*.md"))
	manifest, _ := filepath.Glob(filepath.Join(foreign, "manifest", "*.yaml"))
	before := map[string][]byte{}
	for _, p := range append(paths, manifest...) {
		before[p], _ = os.ReadFile(p)
	}
	err := QuotesToBasis(foreign, QuotesToBasisOptions{Apply: true, Report: filepath.Join(root, "report.json")})
	if err == nil || !strings.Contains(err.Error(), out) || !strings.Contains(err.Error(), owner) || !strings.Contains(err.Error(), foreign) {
		t.Fatalf("expected foreign refusal naming output and codices, got %v", err)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, beforeOut) {
		t.Fatal("output changed")
	}
	for p, b := range before {
		got, _ := os.ReadFile(p)
		if !bytes.Equal(got, b) {
			t.Fatalf("%s changed", p)
		}
	}
}

func TestQuotesForeignTree(t *testing.T) {
	root, owner, out := quoteFixture(t, "Правило (пользователь 03.02: «слово»).\n")
	if err := setManifestHeader(owner, filepath.Base(out), true); err != nil {
		t.Fatal(err)
	}
	if err := Build(owner, false); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(root, "foreign")
	if err := os.CopyFS(foreign, os.DirFS(owner)); err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(root, "tree")
	if err := os.Mkdir(tree, 0755); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, filepath.Base(out)), before, 0644); err != nil {
		t.Fatal(err)
	}
	if err := QuotesToBasis(foreign, QuotesToBasisOptions{Apply: true, OutputsUnder: out, Tree: tree, Report: filepath.Join(root, "report.json")}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, before) {
		t.Fatal("live output changed")
	}
	if _, err := os.Stat(filepath.Join(tree, filepath.Base(out))); err != nil {
		t.Fatalf("tree output missing: %v", err)
	}
}

func TestQuotesForeignOwn(t *testing.T) {
	root, codex, _ := quoteFixture(t, "Правило (пользователь 03.02: «слово»).\n")
	if err := QuotesToBasis(codex, QuotesToBasisOptions{Apply: true, Report: filepath.Join(root, "report.json")}); err != nil {
		t.Fatal(err)
	}
}

func TestBuildForeignNoSymlink(t *testing.T) {
	root := t.TempDir()
	owner := filepath.Join(root, "A")
	out := filepath.Join(root, "source", "OUT.md")
	if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "CLAUDE.md")
	if err := importDataWithLinks(owner, out, "OUT.md", []byte("Body\n"), false, []outputLink{{path: link, target: "source/OUT.md", targetSet: true}}); err != nil {
		t.Fatal(err)
	}
	if err := setManifestHeader(owner, "OUT.md", true); err != nil {
		t.Fatal(err)
	}
	if err := Build(owner, false); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(root, "B")
	if err := os.CopyFS(foreign, os.DirFS(owner)); err != nil {
		t.Fatal(err)
	}
	manifest, err := readManifest(filepath.Join(foreign, "manifest", "OUT.md.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.links) == 0 {
		t.Fatal("fixture has no output symlink")
	}
	for _, outputLink := range manifest.links {
		if err := os.Remove(outputLink.path); err != nil {
			t.Fatal(err)
		}
	}
	if err := Build(foreign, false); err == nil {
		t.Fatal("expected refusal")
	}
	for _, outputLink := range manifest.links {
		if _, err := os.Lstat(outputLink.path); !os.IsNotExist(err) {
			t.Fatalf("link created: %v", err)
		}
	}
}

func TestQuotesKeepConditional(t *testing.T) {
	root, codex, _ := quoteFixture(t, "Правило (пользователь 09.02: «Сделай так» при условии проверки) и (ссылка; пользователь 11.02: «Иначе»).\n")
	reportPath := filepath.Join(root, "dry.json")
	if err := QuotesToBasis(codex, QuotesToBasisOptions{Apply: true, Report: reportPath}); err != nil {
		t.Fatal(err)
	}
	var report quotesReport
	data, err := os.ReadFile(reportPath)
	if err != nil || json.Unmarshal(data, &report) != nil {
		t.Fatalf("read report: %v", err)
	}
	if report.FragmentsMoved != 0 || len(report.Untouched) != 2 {
		t.Fatalf("report = %+v", report)
	}
	joined := report.Untouched[0].Text + report.Untouched[1].Text
	if !strings.Contains(joined, "при условии проверки") || !strings.Contains(joined, "ссылка; пользователь") {
		t.Fatalf("untouched parentheses not listed: %+v", report.Untouched)
	}
}

func TestQuotesRefreshKeepsFields(t *testing.T) {
	root, codex, output := quoteFixture(t, "Повторяемое правило (пользователь 03.02: «Сохрани меня»).\n")
	reportPath := filepath.Join(root, "dry.json")
	if err := QuotesToBasis(codex, QuotesToBasisOptions{Apply: true, Report: reportPath}); err != nil {
		t.Fatal(err)
	}
	fragmentsBefore, err := filepath.Glob(filepath.Join(codex, "frag", "*", "*.md"))
	if err != nil || len(fragmentsBefore) != 1 {
		t.Fatalf("pre-refresh fragments = %v, %v", fragmentsBefore, err)
	}
	before, err := os.ReadFile(fragmentsBefore[0])
	if err != nil || !strings.Contains(string(before), `основание: (пользователь 03.02: «Сохрани меня»)`) {
		t.Fatalf("transfer did not set basis before refresh: %s, %v", before, err)
	}
	if err := ImportWithOptions(codex, output, ImportOptions{Home: root, Refresh: true}); err != nil {
		t.Fatalf("import --refresh: %v", err)
	}
	fragments, err := filepath.Glob(filepath.Join(codex, "frag", "*", "*.md"))
	if err != nil || len(fragments) != 1 {
		t.Fatalf("refreshed fragments = %v, %v", fragments, err)
	}
	fragment, err := os.ReadFile(fragments[0])
	if err != nil || !strings.Contains(string(fragment), `основание: (пользователь 03.02: «Сохрани меня»)`) {
		t.Fatalf("refresh lost basis field: %s, %v", fragment, err)
	}
}

func TestQuotesSpacing(t *testing.T) {
	source := "Перед точкой (пользователь 15.02).\nДве (пользователь 17.02) и (пользователь 19.02).\n(пользователь 21.02) (пользователь 23.02) Начало строки.\nКонец строки (пользователь 25.02)\n"
	root, codex, output := quoteFixture(t, source)
	if err := QuotesToBasis(codex, QuotesToBasisOptions{Apply: true, Report: filepath.Join(root, "report.json")}); err != nil {
		t.Fatal(err)
	}
	built, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	want := "Перед точкой.\nДве и.\nНачало строки.\nКонец строки\n"
	if string(built) != want {
		t.Fatalf("built output = %q, want %q", built, want)
	}
}

func TestQuotesRefuseManualEdit(t *testing.T) {
	root, codex, manualOutput := quoteFixture(t, "Manual output (пользователь 15.02: «do not overwrite»).\n")
	otherOutput := filepath.Join(root, "other.md")
	if err := os.WriteFile(otherOutput, []byte("Other output (пользователь 17.02: «continue»).\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, otherOutput, ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	manualBytes, err := os.ReadFile(manualOutput)
	if err != nil {
		t.Fatal(err)
	}
	manualBytes = append(manualBytes, []byte("manual edit\n")...)
	if err := os.WriteFile(manualOutput, manualBytes, 0644); err != nil {
		t.Fatal(err)
	}
	manualManifest, err := readManifest(filepath.Join(codex, "manifest", codexKey("CLAUDE.md")+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(codex, "manifest", codexKey("CLAUDE.md")+".yaml")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manualFragments := make(map[string][]byte, len(manualManifest.parts))
	for _, id := range manualManifest.parts {
		path := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
		manualFragments[path], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	reportPath := filepath.Join(root, "report.json")
	err = QuotesToBasis(codex, QuotesToBasisOptions{Apply: true, Report: reportPath})
	if err == nil || !strings.Contains(err.Error(), manualOutput) {
		t.Fatalf("apply error = %v, want named manual output", err)
	}
	gotOutput, err := os.ReadFile(manualOutput)
	if err != nil || string(gotOutput) != string(manualBytes) {
		t.Fatalf("manual output changed: %q, %v", gotOutput, err)
	}
	for path, want := range manualFragments {
		got, readErr := os.ReadFile(path)
		if readErr != nil || string(got) != string(want) {
			t.Fatalf("manual fragment changed: %s: %q, %v", path, got, readErr)
		}
	}
	gotManifest, err := os.ReadFile(manifestPath)
	if err != nil || string(gotManifest) != string(manifestBytes) {
		t.Fatalf("manual manifest changed: %q, %v", gotManifest, err)
	}
	var report quotesReport
	reportBytes, err := os.ReadFile(reportPath)
	if err != nil || json.Unmarshal(reportBytes, &report) != nil {
		t.Fatalf("read apply report: %v", err)
	}
	manualReason := false
	for _, entry := range report.Outputs {
		manualReason = manualReason || entry.Output == manualOutput && entry.Reason != ""
	}
	if len(report.Outputs) != 2 || !manualReason {
		t.Fatalf("manual output reason missing from report: %+v", report.Outputs)
	}
	otherFragments, err := filepath.Glob(filepath.Join(codex, "frag", codexKey("other.md"), "*.md"))
	if err != nil || len(otherFragments) != 1 {
		t.Fatalf("other fragments = %v, %v", otherFragments, err)
	}
	otherRaw, err := os.ReadFile(otherFragments[0])
	if err != nil || !strings.Contains(string(otherRaw), `основание: (пользователь 17.02: «continue»)`) {
		t.Fatalf("other output was not transferred: %s, %v", otherRaw, err)
	}
}

func TestQuotesRefreshShift(t *testing.T) {
	root, codex, output := quoteFixture(t, "Абзац один.\n\nАбзац два (пользователь 03.02: «мой источник»).\n")
	if err := QuotesToBasis(codex, QuotesToBasisOptions{Apply: true, Report: filepath.Join(root, "report.json")}); err != nil {
		t.Fatal(err)
	}
	updatedSource := "Новый абзац.\n\nАбзац один.\n\nАбзац два.\n"
	if err := os.WriteFile(output, []byte(updatedSource), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, output, ImportOptions{Home: root, Refresh: true}); err != nil {
		t.Fatalf("import --refresh after inserting paragraph: %v", err)
	}
	fragments, err := filepath.Glob(filepath.Join(codex, "frag", "*", "*.md"))
	if err != nil || len(fragments) != 3 {
		t.Fatalf("refreshed fragments = %v, %v", fragments, err)
	}
	for _, path := range fragments {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		switch {
		case strings.Contains(string(raw), "Абзац два."):
			if !strings.Contains(string(raw), `основание: (пользователь 03.02: «мой источник»)`) {
				t.Fatalf("basis left its original paragraph: %s", raw)
			}
		case strings.Contains(string(raw), "Новый абзац."):
			if !strings.Contains(string(raw), "основание: \n") {
				t.Fatalf("new paragraph inherited an old header: %s", raw)
			}
		}
	}
}

func TestQuotesRefreshEditedKeepsByID(t *testing.T) {
	root, codex, output := quoteFixture(t, "Rule alpha (пользователь 03.02: «мой источник»).\n")
	if err := QuotesToBasis(codex, QuotesToBasisOptions{Apply: true, Report: filepath.Join(root, "report.json")}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("Rule alpha 03.02, уточнено.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, output, ImportOptions{Home: root, Refresh: true}); err != nil {
		t.Fatalf("import --refresh after editing paragraph: %v", err)
	}
	fragments, err := filepath.Glob(filepath.Join(codex, "frag", "*", "*.md"))
	if err != nil || len(fragments) != 1 {
		t.Fatalf("refreshed fragments = %v, %v", fragments, err)
	}
	raw, err := os.ReadFile(fragments[0])
	if err != nil || !strings.Contains(string(raw), "Rule alpha 03.02, уточнено.") || !strings.Contains(string(raw), `основание: (пользователь 03.02: «мой источник»)`) {
		t.Fatalf("edited fragment with the same id lost its header: %s, %v", raw, err)
	}
}

func TestCheckRuleFields(t *testing.T) {
	root, codex, _ := quoteFixture(t, "Правило без заполненных полей.\n\nШаг процедуры без заполненных полей.\n")
	fragments, err := filepath.Glob(filepath.Join(codex, "frag", "*", "*.md"))
	if err != nil || len(fragments) != 2 {
		t.Fatalf("fragments = %v, %v", fragments, err)
	}
	for _, path := range fragments {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if strings.Contains(string(raw), "Правило") {
			raw = []byte(strings.Replace(string(raw), "снять_когда: \n---\n", "снять_когда: \nтип: правило\n---\n", 1))
			if err := os.WriteFile(path, raw, 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil || len(warnings) != 2 {
		t.Fatalf("rule warnings = %v, err = %v", warnings, err)
	}
	if err := Build(codex, true); err != nil {
		t.Fatalf("build --check: %v", err)
	}
	_ = root
}

func TestRuleBasisEmptyBasis(t *testing.T) {
	codex, _, frag := ruleBasisFixture(t, "Правило.\n", "цель: назначение", "снять_когда: условие")
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	assertRuleBasis(t, warnings, err, 1, frag, "основание")
}

func TestRuleBasisEmptyRemove(t *testing.T) {
	codex, _, frag := ruleBasisFixture(t, "Правило.\n", "основание: источник", "снять_когда: ")
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	assertRuleBasis(t, warnings, err, 1, frag, "снять_когда")
}

func TestRuleBasisEmptyPurposeOk(t *testing.T) {
	codex, _, _ := ruleBasisFixture(t, "Правило.\n", "цель: ", "основание: источник", "снять_когда: условие")
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil || RuleBasisWarningCount(warnings) != 0 {
		t.Fatalf("warnings = %v, err = %v", warnings, err)
	}
}

func TestRuleBasisPurposeOnly(t *testing.T) {
	codex, _, frag := ruleBasisFixture(t, "Правило.\n", "цель: назначение")
	raw, err := os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, frag, bytes.Replace(raw, []byte("тип: правило\n"), nil, 1))
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil || RuleBasisWarningCount(warnings) != 1 || len(warnings) != 2 {
		t.Fatalf("warnings = %v, err = %v", warnings, err)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "основание") || !strings.Contains(strings.Join(warnings, "\n"), "снять_когда") {
		t.Fatalf("warnings do not name both empty fields: %v", warnings)
	}
}

func TestRuleBasisPlainFragment(t *testing.T) {
	codex, _, _ := ruleBasisFixture(t, "Обычный текст.\n")
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil || RuleBasisWarningCount(warnings) != 0 {
		t.Fatalf("warnings = %v, err = %v", warnings, err)
	}
}

func TestRuleBasisQuoteInText(t *testing.T) {
	codex, _, frag := ruleBasisFixture(t, "Правило (пользователь 13.02: «цитата»).\n")
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	assertRuleBasis(t, warnings, err, 1, frag, "цитата пользователя в тексте")
}

func TestRuleBasisBlindQuoteForms(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"explanation before colon", "Правило (пользователь 07.03 возле схемы водопровода «клапан у цистерны» перед пометкой о давлении: «цитата»).\n"},
		{"other text in parentheses", "Правило (`decisions.md` sample-entry-one; опись мешков у ворот; пользователь 09.03: «цитата»; 11.03 ведомости).\n"},
		{"newline in quote", "Правило (пользователь 13.03: «ящики следует оставить\n   рядом с грузовым лифтом»).\n"},
		{"time and full date", "Правило: пользователь 15.03 08:40 CET: «цитата»; пользователь 2014-03-07: «другая цитата».\n"},
		{"no parentheses", "Шаблон пользователя 17.03: «тюк укладывать на нижний ярус». Разметка (пользователь 19.03 06:25): «проверить мост перед рейсом».\n"},
		{"other words and undated reply", "Правило: выбор пользователя 21.03 для отгрузки; заказ пользователя 23.03: «цитата»; пользователь ей: «Принято».\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codex, _, frag := ruleBasisFixture(t, tt.body)
			warnings, err := CheckWithWarnings(codex, CheckOptions{})
			assertRuleBasis(t, warnings, err, 1, frag, "цитата пользователя в тексте")
		})
	}
}

func TestRuleBasisQuoteFalsePositives(t *testing.T) {
	texts := []string{
		"по слову пользователя",
		"выбор пользователя",
		"(пользователь вправе взять вторую тачку)",
		"составь меню; какой суп выбрать",
		"Сняты пользователем 07.03 («Лопата для грядок», «Лейка с длинным носиком») из списка инвентаря",
		"слово пользователя 07.03 — на бирке у бочки",
		"слово пользователя 09.03 — на обратной стороне схемы теплицы; отметка рядом задаёт сорт, а не размер горшка",
		"если пользователь вешает бирку «ряд с морковью» либо «место у забора»",
		"пользователь называет «сухой склад», «дальний ряд», «рампа»",
		"пользователь повезёт урожай («корзину к воротам», …)",
		"ключ от сарая держит пользователь (на двери висит табличка: «вернусь к ужину»)",
		"на кухне пользователь отвечает за подачу, затем нарезает зелень … «к обеду»",
		"(в журнале смены пользователь пропущен, стоит отметка «общий склад»)",
		"пользователь стоит у весов, мешок выдают ему с биркой «Груз взвешен»",
		"Отмечает пользователь: «саженцы перенести в тень» мелом на доске у входа",
		"ящики привёз пользователь (`sample-entry-one`, «корзина яблок»)",
		"атлас пользователь оставил у хранителя",
		"пользователь пришёл до открытия зала «Северный»",
	}
	for _, body := range texts {
		t.Run(body, func(t *testing.T) {
			codex, _, _ := ruleBasisFixture(t, "Правило: "+body+".\n")
			warnings, err := CheckWithWarnings(codex, CheckOptions{})
			if err != nil || countWarningsContaining(warnings, "цитата пользователя в тексте") != 0 {
				t.Fatalf("warnings = %v, err = %v", warnings, err)
			}
		})
	}
}

func TestRuleBasisKnownLiveQuoteForms(t *testing.T) {
	texts := []string{
		"(пользователь 25.03 о полках разной высоты: на нижней «атласы и карты»; у окна после перестановки висит указатель «путеводители»)",
		"(пользователь 11.02: расцветки обложек — …)",
		"правило решения - пользователь 05.04: «перед уборкой закрыть окно теплицы. Ключ вернуть сторожу»",
	}
	for _, body := range texts {
		t.Run(body, func(t *testing.T) {
			codex, _, frag := ruleBasisFixture(t, "Правило: "+body+".\n")
			warnings, err := CheckWithWarnings(codex, CheckOptions{})
			assertRuleBasis(t, warnings, err, 1, frag, "цитата пользователя в тексте")
		})
	}
}

func TestRuleBasisSharedFragment(t *testing.T) {
	codex, output, frag := ruleBasisFixture(t, "Правило.\n", "основание: ", "снять_когда: условие")
	secondOut := filepath.Join(filepath.Dir(output), "second.md")
	mustWrite(t, secondOut, []byte("Правило.\n"))
	first, err := readManifest(manifestOf(t, codex, "rules.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := importData(codex, secondOut, "second.md", []byte("Правило.\n"), false); err != nil {
		t.Fatal(err)
	}
	second, err := readManifest(manifestOf(t, codex, "second.md"))
	if err != nil {
		t.Fatal(err)
	}
	secondFragment := filepath.Join(codex, "frag", filepath.FromSlash(second.parts[0])+".md")
	if err := os.Remove(secondFragment); err != nil {
		t.Fatal(err)
	}
	second.parts = []string{first.parts[0]}
	second.fragmentHashes = map[string]string{}
	if err := writeManifest(manifestOf(t, codex, "second.md"), second, nil); err != nil {
		t.Fatal(err)
	}
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	assertRuleBasis(t, warnings, err, 1, frag, "основание")
	if count := countWarningsContaining(warnings, frag+": rule has empty основание"); count != 1 {
		t.Fatalf("empty-basis warning count for shared fragment = %d, warnings = %v", count, warnings)
	}
}

func TestRuleBasisBothEmpty(t *testing.T) {
	codex, _, frag := ruleBasisFixture(t, "Правило.\n", "основание: ", "снять_когда: ")
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil || RuleBasisWarningCount(warnings) != 1 || len(warnings) != 2 {
		t.Fatalf("warnings = %v, rule count = %d, err = %v", warnings, RuleBasisWarningCount(warnings), err)
	}
	if countWarningsContaining(warnings, frag+": rule has empty основание") != 1 ||
		countWarningsContaining(warnings, frag+": rule has empty снять_когда") != 1 {
		t.Fatalf("warnings do not contain both empty fields exactly once: %v", warnings)
	}
}

func ruleBasisFixture(t *testing.T, body string, fields ...string) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	codex, output := filepath.Join(root, "codex"), filepath.Join(root, "rules.md")
	if err := importData(codex, output, "rules.md", []byte(body), false); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(manifestOf(t, codex, "rules.md"))
	if err != nil || len(m.parts) != 1 {
		t.Fatalf("manifest = %#v, err = %v", m, err)
	}
	frag := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md")
	raw, err := os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) > 0 {
		for _, field := range fields {
			key, value, _ := strings.Cut(field, ":")
			old := []byte(key + ": \n")
			updated := []byte(key + ": " + strings.TrimSpace(value) + "\n")
			raw = bytes.Replace(raw, old, updated, 1)
		}
		closeAt := bytes.LastIndex(raw, []byte("---\n"))
		raw = append(bytes.Clone(raw[:closeAt]), append([]byte("тип: правило\n"), raw[closeAt:]...)...)
	}
	mustWrite(t, frag, raw)
	mustWrite(t, output, []byte(body))
	return codex, output, frag
}

func assertRuleBasis(t *testing.T, warnings []string, err error, count int, path, field string) {
	t.Helper()
	if err != nil || RuleBasisWarningCount(warnings) != count {
		t.Fatalf("warnings = %v, rule count = %d, err = %v", warnings, RuleBasisWarningCount(warnings), err)
	}
	for _, warning := range warnings {
		if strings.Contains(warning, path) && strings.Contains(warning, field) {
			return
		}
	}
	t.Fatalf("no warning for %s containing %q: %v", path, field, warnings)
}

func countWarningsContaining(warnings []string, text string) int {
	count := 0
	for _, warning := range warnings {
		if strings.Contains(warning, text) {
			count++
		}
	}
	return count
}
