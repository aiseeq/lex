package lex

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPermanentRuleRemovalWarnings(t *testing.T) {
	cases := []struct {
		name, removal                   string
		permanent, bare, generic, until int
	}{
		{"bare", "не снимается", 1, 1, 0, 0},
		{"bare capitalised with period", "Не снимается.", 1, 1, 0, 0},
		{"generic judgement or insurance", "не снимается: суждение или страховка", 1, 0, 1, 0},
		{"generic insurance after dash", "не снимается — страховка", 1, 0, 1, 0},
		{"generic this is judgement", "Не снимается — это суждение.", 1, 0, 1, 0},
		{"accepted reason", "не снимается — порядок полива решает садовник", 1, 0, 0, 0},
		{"generic word with explanation", "не снимается — страховка от потери урожая", 1, 0, 0, 0},
		{"judgement with explanation", "не снимается: суждение — какую грядку полить, решает садовник", 1, 0, 0, 0},
		{"until with comma", "не снимается, пока теплица без крыши", 0, 0, 0, 1},
		{"until capitalised without comma", "Не снимается пока теплица без крыши.", 0, 0, 0, 1},
		{"removal condition", "снять, когда полив включает таймер", 0, 0, 0, 0},
		{"longer word is not the phrase", "не снимаетсяя", 0, 0, 0, 0},
		{"generic by code", "не снимается кодом", 1, 0, 1, 0},
		{"generic principle", "не снимается — это принцип", 1, 0, 1, 0},
		{"until before the phrase", "пока теплица без крыши — не снимается", 0, 0, 0, 1},
		{"until capitalised before the phrase", "Пока теплица без крыши, не снимается.", 0, 0, 0, 1},
		{"generic inside the value", "Первая фраза не снимается: это страховка.", 1, 0, 1, 0},
		{"reason inside the value", "вторая фраза не снимается: полив по часам решает садовник", 0, 0, 0, 0},
		{"phrase at the end with its reason before", "строка о поливе, кодом целиком не снимается", 0, 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			codex, _, frag := ruleBasisFixture(t, "Поливай грядки утром.\n",
				"цель: урожай не сохнет", "основание: журнал теплицы", "снять_когда: "+tc.removal)
			warnings, err := CheckWithWarnings(codex, CheckOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []struct {
				suffix string
				count  int
			}{
				{permanentRuleNote, tc.permanent},
				{bareRemovalWarning, tc.bare},
				{genericRemovalWarning, tc.generic},
				{untilRemovalWarning, tc.until},
				{emptyRemovalWarning, 0},
			} {
				if got := countWarningsContaining(warnings, frag+want.suffix); got != want.count {
					t.Fatalf("warnings %q: count %d, want %d; warnings = %v", want.suffix, got, want.count, warnings)
				}
			}
			wantTail := fmt.Sprintf("; постоянных правил: %d, из них без причины: %d, с общей причиной: %d; «не снимается, пока»: %d; пустых фрагментов: 0; пустых разделителей: 0", tc.permanent, tc.bare, tc.generic, tc.until)
			if got := RuleWarningSummary(warnings); !strings.HasSuffix(got, wantTail) {
				t.Fatalf("summary = %q, want suffix %q", got, wantTail)
			}
		})
	}
}

func TestPermanentRuleNoteIsHiddenFromPrintedWarnings(t *testing.T) {
	codex, _, frag := ruleBasisFixture(t, "Поливай грядки утром.\n",
		"цель: урожай не сохнет", "основание: журнал теплицы", "снять_когда: не снимается — порядок полива решает садовник")
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	notes := 0
	for _, warning := range warnings {
		if IsPermanentRuleNote(warning) {
			notes++
			if warning != frag+permanentRuleNote {
				t.Fatalf("note = %q, want %q", warning, frag+permanentRuleNote)
			}
		}
	}
	if notes != 1 {
		t.Fatalf("permanent rule notes = %d, want 1; warnings = %v", notes, warnings)
	}
	if IsPermanentRuleNote(frag + bareRemovalWarning) {
		t.Fatal("a printed warning is taken for the hidden note")
	}
}

func TestEmptyFragmentWarning(t *testing.T) {
	ruleFields := []string{"цель: урожай не сохнет", "основание: журнал теплицы", "снять_когда: снять, когда полив включает таймер"}
	cases := []struct {
		name, body       string
		fields           []string
		empty, separator int
	}{
		{"fields and newline only", "\n", ruleFields, 1, 0},
		{"fields and blank lines with spaces", "\n  \n\t\n", ruleFields, 1, 0},
		{"fields and text", "Поливай грядки утром.\n", ruleFields, 0, 0},
		{"no fields and newline only", "\n", nil, 0, 1},
		{"no fields and blank lines", "\n\n", nil, 0, 1},
		{"no fields and no text", "", nil, 1, 0},
		{"no fields and text", "Поливай грядки утром.\n", nil, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			codex, _, frag := ruleBasisFixture(t, "Поливай грядки утром.\n", tc.fields...)
			raw, err := os.ReadFile(frag)
			if err != nil {
				t.Fatal(err)
			}
			header := fragmentHeader(raw)
			mustWrite(t, frag, append(bytes.Clone(header), tc.body...))
			if err := BuildWithOptions(codex, BuildOptions{Force: true}); err != nil {
				t.Fatal(err)
			}
			warnings, err := CheckWithWarnings(codex, CheckOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if got := countWarningsContaining(warnings, frag+emptyFragmentWarning); got != tc.empty {
				t.Fatalf("empty fragment warnings = %d, want %d; warnings = %v", got, tc.empty, warnings)
			}
			if got := countWarningsContaining(warnings, frag+blankSeparatorWarning); got != tc.separator {
				t.Fatalf("blank separator warnings = %d, want %d; warnings = %v", got, tc.separator, warnings)
			}
			wantTail := fmt.Sprintf("; пустых фрагментов: %d; пустых разделителей: %d", tc.empty, tc.separator)
			if got := RuleWarningSummary(warnings); !strings.HasSuffix(got, wantTail) {
				t.Fatalf("summary = %q, want suffix %q", got, wantTail)
			}
		})
	}
}

func TestEmptyFragmentWithoutHeaderIsNotWarned(t *testing.T) {
	warnings := ruleFieldWarnings("/codex/frag/item.md", []byte("\n"))
	if countWarningsContaining(warnings, emptyFragmentWarning) != 0 || countWarningsContaining(warnings, blankSeparatorWarning) != 0 {
		t.Fatalf("warnings = %v, want no empty fragment warning without a header", warnings)
	}
}

func TestNonRuleKindIsNotCountedAsPermanent(t *testing.T) {
	codex, output, frag := ruleBasisFixture(t, "Справка о поливе теплицы.\n")
	raw, err := os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("снять_когда: \n"), []byte("снять_когда: не снимается\n"), 1)
	closeAt := bytes.LastIndex(raw, []byte("---\n"))
	raw = append(bytes.Clone(raw[:closeAt]), append([]byte("вид: справка\n"), raw[closeAt:]...)...)
	mustWrite(t, frag, raw)
	mustWrite(t, output, []byte("Справка о поливе теплицы.\n"))
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if countWarningsContaining(warnings, permanentRuleNote) != 0 || countWarningsContaining(warnings, bareRemovalWarning) != 0 {
		t.Fatalf("a fragment with вид is checked as a permanent rule: %v", warnings)
	}
	if got := RuleWarningSummary(warnings); !strings.Contains(got, "; постоянных правил: 0, из них без причины: 0,") {
		t.Fatalf("summary = %q", got)
	}
}

func TestBlankSeparatorInDecisionJournalIsWarned(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	decisionOutput := filepath.Join(root, "decisions.md")
	if err := importData(codex, decisionOutput, "decisions.md", []byte("- решение о поливе\n"), false); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(manifestOf(t, codex, "decisions.md"))
	if err != nil || len(m.parts) != 1 {
		t.Fatalf("manifest parts = %v, err = %v", m.parts, err)
	}
	frag := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md")
	raw, err := os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, frag, append(bytes.Clone(fragmentHeader(raw)), '\n'))
	if err := BuildWithOptions(codex, BuildOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	warnings, err := CheckWithWarnings(codex, CheckOptions{Decisions: decisionOutput})
	if err != nil {
		t.Fatal(err)
	}
	if got := countWarningsContaining(warnings, frag+blankSeparatorWarning); got != 1 {
		t.Fatalf("blank separator warnings in the journal = %d, want 1; warnings = %v", got, warnings)
	}
	if got := RuleWarningSummary(warnings); !strings.HasSuffix(got, "; пустых фрагментов: 0; пустых разделителей: 1") {
		t.Fatalf("summary = %q", got)
	}
}

func TestSharedPermanentRuleIsCountedOnce(t *testing.T) {
	codex, output, frag := ruleBasisFixture(t, "Правило.\n",
		"цель: урожай не сохнет", "основание: журнал теплицы", "снять_когда: не снимается")
	shareFixtureFragment(t, codex, output, "Правило.\n")
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if countWarningsContaining(warnings, frag+permanentRuleNote) != 1 || countWarningsContaining(warnings, frag+bareRemovalWarning) != 1 {
		t.Fatalf("shared permanent rule counted more than once: %v", warnings)
	}
	if got := RuleWarningSummary(warnings); !strings.Contains(got, "; постоянных правил: 1, из них без причины: 1, с общей причиной: 0;") {
		t.Fatalf("summary = %q", got)
	}
}
