package lex

import (
	"strings"
	"testing"
)

func TestRuleGoalRestatesBody(t *testing.T) {
	cases := []struct {
		name, body, goal string
		want             bool
	}{
		{"same wording", "Пиши порядок выдачи ключей в журнале.\n", "Пиши порядок выдачи ключей.", true},
		{"inflected verbs", "Пиши порядок выдачи ключей в журнале.\n", "Пишется порядок выдачи ключей.", true},
		{"inflected nouns", "Сам пользователь фиксирует пароль в журнале.\n", "Самим пользователем фиксируется пароль в журнале.", true},
		{"different failure", "Пиши порядок выдачи ключей в журнале.\n", "Не теряй журнал при переезде.", false},
		{"empty goal", "Пиши порядок выдачи ключей в журнале.\n", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			codex, _, frag := ruleBasisFixture(t, tc.body,
				"цель: "+tc.goal, "основание: учебная схема", "снять_когда: после проверки")
			warnings, err := CheckWithWarnings(codex, CheckOptions{})
			if err != nil {
				t.Fatal(err)
			}
			count := countWarningsContaining(warnings, frag+goalRestatesWarning)
			wantCount := 0
			if tc.want {
				wantCount = 1
			}
			if count != wantCount {
				t.Fatalf("restatement warnings = %v, want count %d", warnings, wantCount)
			}
			if tc.name == "empty goal" && countWarningsContaining(warnings, frag+": rule has empty цель") != 1 {
				t.Fatalf("empty goal warning missing: %v", warnings)
			}
			wantTail := "; цель пересказывает пункт: " + map[bool]string{true: "1", false: "0"}[tc.want]
			if !strings.HasSuffix(RuleWarningSummary(warnings), wantTail) {
				t.Fatalf("summary = %q, want suffix %q", RuleWarningSummary(warnings), wantTail)
			}
		})
	}
}
