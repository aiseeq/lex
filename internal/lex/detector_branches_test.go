package lex

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestUserQuoteDetectorBranches(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"b01_template", "шаблон пользователя 03.02", true},
		{"b02_choice", "выбор пользователя 03.02", true},
		{"b03_order", "заказ пользователя 03.02", true},
		{"b04_term_srok", "срок пользователя 03.02", true},
		{"b05_reply", "реплика пользователя 03.02", true},
		{"b06_clarification", "уточнение пользователя 03.02", true},
		{"b07_answer", "ответ пользователя 03.02", true},
		{"b08_iso_date", "пользователь 2018-03-07", true},
		{"b09_colleague_feminine", "пользователь ей: «полив»", true},
		{"b10_colleague_masculine", "пользователь ему: «ящик»", true},
		{"b11_word_pointer_excluded", "слово пользователя 03.02 «ящик»", false},
		{"b12_word_pointer_excluded_other_quote", "слово пользователя 03.02 «урожай»", false},
		{"b13_undated_colleague", "пользователь ему: «переставь горшок»", true},
		{"b16_date_before_user", "03.02 пользователь «ящик»", false},
		{"b17_quote_before_date", "пользователь «ящик» 03.02", false},
		{"b18_user_mention_form", "пользователю садовода 03.02 «грядка»", true},
		{"b19_user_word_boundary", "пользовательский список 03.02 «ящик»", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasUserQuoteInText([]byte(tt.text)); got != tt.want {
				t.Fatalf("hasUserQuoteInText(%q) = %t, want %t", tt.text, got, tt.want)
			}
		})
	}

	for _, tc := range []struct {
		name  string
		count int
		want  bool
	}{
		{"b14_window_date_30", 30, true},
		{"b14_window_date_31", 31, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := "пользователь " + strings.Repeat("а", tc.count-len([]rune("пользователь "))) + "03.02 «сад»"
			if got := hasUserQuoteInText([]byte(text)); got != tc.want {
				t.Fatalf("rune distance %d: hasUserQuoteInText = %t, want %t", tc.count, got, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name  string
		count int
		want  bool
	}{
		{"b15_window_quote_110", 110, true},
		{"b15_window_quote_111", 111, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prefix := "пользователя 03.02"
			text := prefix + strings.Repeat("а", tc.count-len([]rune(prefix))) + "«сад»"
			if got := hasUserQuoteInText([]byte(text)); got != tc.want {
				t.Fatalf("rune distance %d: hasUserQuoteInText = %t, want %t", tc.count, got, tc.want)
			}
		})
	}
}

func TestTextReferenceDetectorBranches(t *testing.T) {
	journal := filepath.Join(t.TempDir(), "decisions.md")
	tests := []struct {
		name      string
		path      string
		text      string
		journal   string
		wantKind  int
		wantText  string
		wantCount int
	}{
		{"b20_dismissed_snat", "source.md", "Снят `sample-entry-snat`", "## sample-entry-kept\n", 1, "sample-entry-snat", 1},
		{"b21_context_zapis", "source.md", "Записан `sample-entry-zapis`", "## sample-entry-kept\n", 1, "sample-entry-zapis", 1},
		{"b22_more_below", "source.md", "подробнее ниже", "", 2, "нет раздела ниже", 1},
		{"b23_quoted_item", "source.md", "пункт «первый ящик»", "", 2, "первый ящик", 1},
		{"b24_quoted_rule", "source.md", "правило «полив вечером»", "", 2, "полив вечером", 1},
		{"b25_historical_removal_window", "source.md", "Снимает со списка хранения старый садовый вариант, который больше не применяют, правило «сухой погреб»", "", 0, "", 0},
		{"b26_historical_status_uprazdn", "source.md", "Правило «порядок ящиков» упразднено", "", 0, "", 0},
		{"b27_historical_status_otmen", "source.md", "Правило «порядок грядок» отменено", "", 0, "", 0},
		{"b29_markdown_anchor", "source.md", "[сводка](missing.md#итоги)", "", 3, "нет файла", 1},
		{"b30_list_parenthesis", "source.md", "1) Первый ящик\n3) Третий ящик", "", 4, "ожидался пункт 2", 1},
		{"b31_heading_level_four", "source.md", "#### 1. Первый раздел\n#### 3. Третий раздел", "", 4, "ожидался раздел 2", 1},
		{"b32_step_conjunction", "source.md", "Шаги 1 и 3 по саду", "", 4, "нет шага 3", 1},
		{"b33_step_en_dash", "source.md", "Шаги 1–3 по складу", "", 4, "нет шага 2", 1},
		{"b34_file_nearby", "source.md", "`sample-entry-note.md` рядом", "", 3, "нет файла", 1},
		{"b35_decisions_without_extension", "source.md", "decisions sample-entry-absent", "## sample-entry-kept\n", 1, "sample-entry-absent", 1},
		{"b36_decision_list_comma", "source.md", "decisions.md sample-entry-kept, sample-entry-absent", "## sample-entry-kept\n", 1, "sample-entry-absent", 1},
		{"b37_no_ceiling_without_colon", "source.md", "не потолок `sample-entry-absent`", "## sample-entry-kept\n", 1, "sample-entry-absent", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			corpus := textCorpus{filepath.Join(t.TempDir(), tt.path): []byte(tt.text)}
			journalPath := journal
			if tt.journal != "" {
				corpus[journalPath] = []byte(tt.journal)
			}
			findings := withoutOrphanSkillFindings(findTextRefs(corpus, journalPath))
			if tt.wantCount == 0 {
				if len(findings) != 0 {
					t.Fatalf("unexpected findings: %+v", findings)
				}
				return
			}
			count := 0
			for _, finding := range findings {
				if finding.kind == tt.wantKind && strings.Contains(finding.missing, tt.wantText) {
					count++
				}
			}
			if count != tt.wantCount {
				t.Fatalf("findings %+v: wanted %d kind %d containing %q", findings, tt.wantCount, tt.wantKind, tt.wantText)
			}
		})
	}

	t.Run("b25_historical_removal_window_long", func(t *testing.T) {
		text := "Снимает " + strings.Repeat("садовый запас ", 5) + "правило «сухой погреб»"
		path := filepath.Join(t.TempDir(), "source.md")
		findings := withoutOrphanSkillFindings(findTextRefs(textCorpus{path: []byte(text)}, journal))
		if len(findings) != 0 {
			t.Fatalf("historical removal was treated as a section reference: %+v", findings)
		}
	})

	t.Run("b28_register_from_section", func(t *testing.T) {
		dir := t.TempDir()
		source := filepath.Join(dir, "notes.md")
		skill := filepath.Join(dir, "SKILL.md")
		corpus := textCorpus{
			source: []byte("Регистр из раздела «Нет такого»\n"),
			skill:  []byte("## Полив\n"),
		}
		findings := withoutOrphanSkillFindings(findTextRefs(corpus, journal))
		if len(findings) != 1 || findings[0].kind != 2 || !strings.Contains(findings[0].missing, "Нет такого") {
			t.Fatalf("missing register section finding: %+v", findings)
		}
	})

	t.Run("b29_markdown_anchor_existing_file", func(t *testing.T) {
		dir := t.TempDir()
		source := filepath.Join(dir, "source.md")
		target := filepath.Join(dir, "sample-entry-summary.md")
		corpus := textCorpus{
			source: []byte("[сводка](sample-entry-summary.md#итоги)\n"),
			target: []byte("Текст без заголовков.\n"),
		}
		findings := findTextRefs(corpus, journal)
		if len(findings) != 1 || findings[0].kind != 2 || !strings.Contains(findings[0].missing, "итоги") {
			t.Fatalf("missing anchor finding: %+v", findings)
		}
	})

}
