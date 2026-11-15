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
	if len(goalOnlyWarnings) != 0 {
		t.Fatalf("цель alone made a fragment a rule: %v", goalOnlyWarnings)
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
	if got := RuleWarningSummary(warnings); !strings.HasSuffix(got, "пунктов без полей: 2") {
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
