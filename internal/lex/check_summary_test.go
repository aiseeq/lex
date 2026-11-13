package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuleWarningSummaryCountsEachWarningKind(t *testing.T) {
	root := t.TempDir()
	codex, output := filepath.Join(root, "codex"), filepath.Join(root, "rules.md")
	body := []byte("Правило о хранении ящиков.\n\nПравило о поливе теплицы.\n\nПравило о маркировке грядок.\n")
	if err := importData(codex, output, "rules.md", body, false); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, output, body)
	m, err := readManifest(manifestOf(t, codex, "rules.md"))
	if err != nil || len(m.parts) != 3 {
		t.Fatalf("manifest parts = %v, err = %v", m.parts, err)
	}
	for i, fields := range [][]string{
		{"снять_когда: после разгрузки"},
		{"основание: журнал полива"},
		{"основание: схема посадок"},
	} {
		frag := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[i])+".md")
		raw, err := os.ReadFile(frag)
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range fields {
			key, _, _ := strings.Cut(field, ":")
			raw = bytes.Replace(raw, []byte(key+": \n"), []byte(field+"\n"), 1)
		}
		mustWrite(t, frag, raw)
	}
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := "правил без основания: 1, без условия снятия: 2; фрагментов с цитатой пользователя в тексте: 0"
	if got := RuleWarningSummary(warnings); got != want {
		t.Fatalf("summary = %q, want %q (warnings: %v)", got, want, warnings)
	}
}

func TestRuleWarningSummaryBothFieldsEmpty(t *testing.T) {
	codex, _, _ := ruleBasisFixture(t, "Правило о перевозке урожая.\n", "основание: ", "снять_когда: ")
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := "правил без основания: 1, без условия снятия: 1; фрагментов с цитатой пользователя в тексте: 0"
	if got := RuleWarningSummary(warnings); got != want {
		t.Fatalf("summary = %q, want %q (warnings: %v)", got, want, warnings)
	}
}

func TestRuleWarningSummaryQuoteOnly(t *testing.T) {
	codex, _, _ := ruleBasisFixture(t, "Правило (пользователь 03.02: «ящики у ворот»).\n", "основание: схема склада", "снять_когда: после уборки")
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := "правил без основания: 0, без условия снятия: 0; фрагментов с цитатой пользователя в тексте: 1"
	if got := RuleWarningSummary(warnings); got != want {
		t.Fatalf("summary = %q, want %q (warnings: %v)", got, want, warnings)
	}
}

func TestRuleWarningSummaryEmpty(t *testing.T) {
	want := "правил без основания: 0, без условия снятия: 0; фрагментов с цитатой пользователя в тексте: 0"
	if got := RuleWarningSummary(nil); got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
}
