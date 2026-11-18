package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuleFieldWarningsEmptyGoalOnlyForRules(t *testing.T) {
	codex, _, rule := ruleBasisFixture(t, "Правило без цели.\n", "цель: ", "основание: источник", "снять_когда: условие")
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if countWarningsContaining(warnings, rule+": rule has empty цель") != 1 {
		t.Fatalf("warnings = %v", warnings)
	}
	if got := RuleWarningSummary(warnings); !strings.Contains(got, "правил без цели: 1") {
		t.Fatalf("summary = %q", got)
	}

	plainCodex, _, plain := ruleBasisFixture(t, "Обычный фрагмент.\n")
	plainWarnings, err := CheckWithWarnings(plainCodex, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if countWarningsContaining(plainWarnings, plain+": rule has empty цель") != 0 {
		t.Fatalf("non-rule fragment warned about its цель: %v", plainWarnings)
	}
	goalOnlyCodex, _, goalOnly := ruleBasisFixture(t, "Текст.\n", "цель: назначение")
	raw, err := os.ReadFile(goalOnly)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, goalOnly, bytes.Replace(raw, []byte("тип: правило\n"), nil, 1))
	goalOnlyWarnings, err := CheckWithWarnings(goalOnlyCodex, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// цель alone keeps a fragment a rule (check-rule-basis, 12.11): its other fields are reported, its цель is not.
	if countWarningsContaining(goalOnlyWarnings, goalOnly+": rule has empty цель") != 0 ||
		countWarningsContaining(goalOnlyWarnings, goalOnly+": rule has empty основание") != 1 ||
		countWarningsContaining(goalOnlyWarnings, goalOnly+": rule has empty снять_когда") != 1 {
		t.Fatalf("цель-only rule warnings = %v", goalOnlyWarnings)
	}
}

func TestRuleFieldWarningsExcludeKindsHeadingsAndSkillHeaders(t *testing.T) {
	cases := []struct {
		name, body, header string
	}{
		{"kind", "Текст шага.\n", "вид: шаг\n"},
		{"heading", "## Раздел\n\n### Подраздел\n", ""},
		{"skill front matter", "\n---\nname: demo\ndescription: skill\n---\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			codex, output, frag := ruleBasisFixture(t, "обычный текст\n", "цель: ", "основание: источник", "снять_когда: ")
			raw, err := os.ReadFile(frag)
			if err != nil {
				t.Fatal(err)
			}
			closeAt := bytes.LastIndex(raw, []byte("---\n"))
			if tc.header != "" {
				raw = bytes.Replace(raw, []byte("тип: правило\n"), nil, 1)
				closeAt = bytes.LastIndex(raw, []byte("---\n"))
			}
			raw = append(bytes.Clone(raw[:closeAt]), append([]byte(tc.header+"---\n"+tc.body), []byte{}...)...)
			mustWrite(t, frag, raw)
			mustWrite(t, output, []byte(tc.body))
			warnings, err := CheckWithWarnings(codex, CheckOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"цель", "основание", "снять_когда"} {
				if countWarningsContaining(warnings, frag+": rule has empty "+field) != 0 {
					t.Fatalf("%s warned for %s: %v", tc.name, field, warnings)
				}
			}
			if strings.Contains(RuleWarningSummary(warnings), "правил без цели: 1") {
				t.Fatalf("summary = %s", RuleWarningSummary(warnings))
			}
		})
	}
	if fragmentIsNotRule([]byte("---\nid: x\nцель: \nоснование: след\nснять_когда: \n---\n"), []byte("Jira и коммиты\n")) {
		t.Fatal("plain text without # was treated as a heading")
	}
}

func TestRuleAndKindConflictIsProblem(t *testing.T) {
	codex, _, frag := ruleBasisFixture(t, "Текст.\n", "цель: ", "основание: ", "снять_когда: ")
	raw, err := os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	closeAt := bytes.LastIndex(raw, []byte("---\n"))
	raw = append(bytes.Clone(raw[:closeAt]), append([]byte("вид: шаг\n"), raw[closeAt:]...)...)
	mustWrite(t, frag, raw)
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err == nil || !strings.Contains(err.Error(), "тип") || !strings.Contains(err.Error(), "вид") {
		t.Fatalf("check error = %v, want problem naming тип and вид", err)
	}
	if countWarningsContaining(warnings, frag+": rule has empty") != 0 {
		t.Fatalf("conflicting header emitted rule warnings: %v", warnings)
	}
}

func TestDecisionManifestItemsAreExcludedFromUnmarkedCount(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	decisionOutput := filepath.Join(root, "decisions.md")
	otherOutput := filepath.Join(root, "other.md")
	if err := importData(codex, decisionOutput, "decisions.md", []byte("- decision item\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := importData(codex, otherOutput, "other.md", []byte("- instruction item\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	warnings, err := CheckWithWarnings(codex, CheckOptions{Decisions: decisionOutput})
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(RuleWarningSummary(warnings), "пунктов без полей: 1; цель пересказывает пункт: 0") == false {
		t.Fatalf("summary = %q, warnings = %v", RuleWarningSummary(warnings), warnings)
	}
	if countWarningsContaining(warnings, itemHasNoFieldsWarning) != 1 {
		t.Fatalf("unmarked warnings = %v", warnings)
	}
	for _, warning := range warnings {
		if strings.Contains(warning, filepath.Join(codex, "frag", "decisions")) && strings.HasSuffix(warning, itemHasNoFieldsWarning) {
			t.Fatalf("decision item printed as unmarked: %v", warnings)
		}
	}
}

func TestRuleTypedListItemIsNotUnmarked(t *testing.T) {
	codex, _, rule := ruleBasisFixture(t, "- Правило пунктом.\n", "цель: ", "основание: ", "снять_когда: ")
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if countWarningsContaining(warnings, rule+": rule has empty цель") != 1 || countWarningsContaining(warnings, rule+itemHasNoFieldsWarning) != 0 {
		t.Fatalf("typed rule list item warnings = %v", warnings)
	}
}

func TestUnmarkedListItemsAndKinds(t *testing.T) {
	root := t.TempDir()
	codex, output := filepath.Join(root, "codex"), filepath.Join(root, "list.md")
	if err := importData(codex, output, "list.md", []byte("- dash item\n1. numbered item\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(manifestOf(t, codex, "list.md"))
	if err != nil {
		t.Fatal(err)
	}
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if countWarningsContaining(warnings, itemHasNoFieldsWarning) != 2 {
		t.Fatalf("item warnings = %v, parts = %v", warnings, m.parts)
	}
	if got := RuleWarningSummary(warnings); !strings.HasSuffix(got, "пунктов без полей: 2; цель пересказывает пункт: 0") {
		t.Fatalf("summary = %q", got)
	}
	baseRaw := make(map[string][]byte, len(m.parts))
	for _, id := range m.parts {
		path := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
		baseRaw[id], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []string{"шаг", "указатель", "справка"} {
		for _, id := range m.parts {
			path := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
			raw := bytes.Clone(baseRaw[id])
			raw = bytes.Replace(raw, []byte("снять_когда: \n"), []byte("снять_когда: \nвид: "+kind+"\n"), 1)
			if _, err := fragmentBody(raw); err != nil {
				t.Fatalf("вид %q rejected: %v", kind, err)
			}
			if !bytes.Contains(raw, []byte("вид: "+kind+"\n")) {
				t.Fatalf("вид %q not retained in header", kind)
			}
			if err := os.WriteFile(path, raw, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		markedWarnings, err := CheckWithWarnings(codex, CheckOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if countWarningsContaining(markedWarnings, itemHasNoFieldsWarning) != 0 {
			t.Fatalf("вид %q did not exclude marked fragment: %v", kind, markedWarnings)
		}
	}
	for _, rawKind := range []string{"вид: \n", "вид:\n"} {
		raw := bytes.Replace(bytes.Clone(baseRaw[m.parts[0]]), []byte("снять_когда: \n"), []byte("снять_когда: \n"+rawKind), 1)
		body, err := fragmentBody(raw)
		if err != nil {
			t.Fatalf("empty вид %q rejected: %v", rawKind, err)
		}
		if !fragmentHasNoFields(raw, body) {
			t.Fatalf("empty вид %q did not leave fragment unmarked", rawKind)
		}
	}
}

func TestUnknownFragmentKindRejected(t *testing.T) {
	raw := []byte("---\nid: x\nцель: \nоснование: \nснять_когда: \nвид: неизвестно\n---\n- пункт\n")
	if _, err := fragmentBody(raw); err == nil || !strings.Contains(err.Error(), "вид") {
		t.Fatalf("fragmentBody error = %v, want an error naming вид", err)
	}
}

func TestAbsorbPreservesFragmentKind(t *testing.T) {
	root := t.TempDir()
	codex, output := filepath.Join(root, "codex"), filepath.Join(root, "steps.md")
	body := []byte("- first step\n- second step\n")
	if err := importData(codex, output, "steps.md", body, false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	parts, err := readManifest(manifestOf(t, codex, "steps.md"))
	if err != nil {
		t.Fatal(err)
	}
	frag := filepath.Join(codex, "frag", filepath.FromSlash(parts.parts[0])+".md")
	raw, err := os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("снять_когда: \n"), []byte("снять_когда: \nвид: шаг\n"), 1)
	if err := os.WriteFile(frag, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	built, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(built, []byte("вид:")) {
		t.Fatal("вид leaked into assembled output")
	}
	built = bytes.Replace(built, []byte("first step"), []byte("first step edited"), 1)
	if err := os.WriteFile(output, built, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Absorb(codex, output, AbsorbOptions{}); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(fragmentHeader(raw), []byte("вид: шаг\n")) {
		t.Fatalf("absorb lost вид: шаг: %s", raw)
	}
}

func TestImportRefreshPreservesFragmentKind(t *testing.T) {
	root, codex, output := quoteFixture(t, "- first step\n")
	m, err := readManifest(manifestOf(t, codex, "CLAUDE.md"))
	if err != nil || len(m.parts) != 1 {
		t.Fatalf("manifest parts = %v, err = %v", m.parts, err)
	}
	frag := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md")
	raw, err := os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("снять_когда: \n"), []byte("снять_когда: \nвид: шаг\n"), 1)
	if err := os.WriteFile(frag, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, output, ImportOptions{Home: root, Refresh: true}); err != nil {
		t.Fatalf("import --refresh: %v", err)
	}
	raw, err = os.ReadFile(frag)
	if err != nil || !bytes.Contains(fragmentHeader(raw), []byte("вид: шаг\n")) {
		t.Fatalf("refresh lost вид: шаг: %s, err = %v", raw, err)
	}
}

func decisionAndOtherCodex(t *testing.T, root string) (codex, decisionOutput, otherOutput string) {
	t.Helper()
	codex = filepath.Join(root, "codex")
	decisionOutput = filepath.Join(root, "decisions.md")
	otherOutput = filepath.Join(root, "other.md")
	if err := importData(codex, decisionOutput, "decisions.md", []byte("- decision item\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := importData(codex, otherOutput, "other.md", []byte("- instruction item\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	return codex, decisionOutput, otherOutput
}

func TestBrokenToolsJSONKeepsDiskCheckAndItemCount(t *testing.T) {
	codex, _, otherOutput := decisionAndOtherCodex(t, t.TempDir())
	mustWrite(t, filepath.Join(codex, "tools.json"), []byte(`{"text_refs":{"exceptions":[{"path":"/abs","kind":1,"contains":"x","reason":"y"}]}}`))
	mustWrite(t, otherOutput, []byte("- instruction item, edited by hand\n"))
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err == nil || !strings.Contains(err.Error(), "built output differs") {
		t.Fatalf("check error = %v, want the disk drift of other.md", err)
	}
	if got := RuleWarningSummary(warnings); !strings.HasSuffix(got, "пунктов без полей: 1; цель пересказывает пункт: 0") {
		t.Fatalf("summary = %q, want the instruction item counted and the journal (default path) left out", got)
	}
}

func TestDecisionJournalNamedThroughSymlinkIsExcluded(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	codex, _, _ := decisionAndOtherCodex(t, real)
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	warnings, err := CheckWithWarnings(codex, CheckOptions{Decisions: filepath.Join(alias, "decisions.md")})
	if err != nil {
		t.Fatal(err)
	}
	if got := RuleWarningSummary(warnings); !strings.HasSuffix(got, "пунктов без полей: 1; цель пересказывает пункт: 0") {
		t.Fatalf("summary = %q, warnings = %v", got, warnings)
	}
}
