package lex

import (
	"strings"
	"testing"
)

func TestGoalRestatesBody(t *testing.T) {
	cases := []struct {
		name, body, goal string
		want             bool
	}{
		{"same wording", "Пиши порядок выдачи ключей в журнале.\n", "Пиши порядок выдачи ключей.", true},
		{"inflected verbs", "Пиши неси.\n", "Пишется несётся.", true},
		{"inflected nouns", "Сам пользователь фиксирует пароль в журнале.\n", "Самим пользователем фиксируется пароль в журнале.", true},
		{"different failure", "Пиши порядок выдачи ключей в журнале.\n", "Не теряй журнал при переезде.", false},
		{"empty goal", "Пиши порядок выдачи ключей в журнале.\n", "", false},
		{
			"long body",
			"Сохраняй маршрут курьера внутри архива. Затем проверяй перечень комнат перед обходом. Записывай номер тележки в журнал. Отмечай погоду возле ворот.\n",
			"Сохраняй маршрут курьера внутри архива.",
			true,
		},
		{"below half threshold", "Баркас тополь мандарин кофевар термос.\n", "Баркас тополь лира вектор табурет.", false},
		{"above half threshold", "Баркас тополь лира чужбина ракета.\n", "Баркас тополь лира вектор табурет.", true},
		{"exactly half threshold", "Баркас тополь мандарин.\n", "Баркас тополь лира вектор.", true},
		{"stems equal in five letters", "Ракетный баркас.\n", "Ракетка баркас.", true},
		{"only one common stem", "Баркас лежит рядом.\n", "Баркас тополь.", false},
		{"goal words in separate sentences", "Баркас лежит рядом. Тополь растёт отдельно.\n", "Баркас тополь лира.", false},
		{"different five letter stems", "Провайдер архив грузовик.\n", "Проверка архив компас.", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			codex, _, frag := ruleBasisFixture(t, tc.body,
				"цель: "+tc.goal, "основание: выдуманная схема", "снять_когда: после проверки")
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
			wantTail := "; цель пересказывает пункт: " + map[bool]string{true: "1", false: "0"}[tc.want] + "; ссылок по положению: 0; постоянных правил: 0, из них без причины: 0, с общей причиной: 0; «не снимается, пока»: 0; пустых фрагментов: 0; пустых разделителей: 0"
			if !strings.HasSuffix(RuleWarningSummary(warnings), wantTail) {
				t.Fatalf("summary = %q, want suffix %q", RuleWarningSummary(warnings), wantTail)
			}
		})
	}
}
