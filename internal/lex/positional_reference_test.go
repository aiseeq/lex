package lex

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestPositionalReference(t *testing.T) {
	cases := []struct {
		name, text, want string
	}{
		{"prepositional line above", "Учитывай правило по строке выше.", "по строке выше"},
		{"bare line above", "Строка выше задаёт порядок.", "Строка выше"},
		{"bare point above", "Пункт выше задаёт порядок.", "Пункт выше"},
		{"line in accusative context", "Перейди в пункт ниже.", "в пункт ниже"},
		{"below after two words", "Ищи описание по строке о моделях ниже.", "по строке о моделях ниже"},
		{"below after one word", "Сверь формулировку на пункт о письмах ниже.", "на пункт о письмах ниже"},
		{"inflected previous", "Следуй предыдущему пункту.", "предыдущему пункту"},
		{"inflected next", "Опиши следующей строкой ответ.", "следующей строкой"},
		{"newline between next adjective and unit", "Добавь следующей\nстрокой метку", ""},
		{"newline between unit and below", "Проверь по абзацу\nниже", ""},
		{"punctuation between next adjective and unit", "Сверь с предыдущему, разделу пометку", ""},
		{"tab is flattened in warning", "Сверь по строке\tниже.", "по строке ниже"},
		{"paragraph above", "Сводка в абзаце приведена выше.", "в абзаце приведена выше"},
		{"section below", "Описание в разделе о настройке ниже.", "в разделе о настройке ниже"},
		{"three words between unit and above", "Ищи в пункте о хранении копий выше.", "в пункте о хранении копий выше"},
		{"four words between unit and above", "Пункт размещения копий новых форм выше.", ""},
		{"rule nominative", "Основное правило размещено ниже.", "правило размещено ниже"},
		{"rule ending in a", "Название правила приведено выше.", "правила приведено выше"},
		{"rule genitive plural", "Перечень местных правил приведён выше.", "правил приведён выше"},
		{"rule plural accusative", "Сверь правила ниже.", "правила ниже"},
		{"rule dative singular", "Пометка к правилу ниже.", "к правилу ниже"},
		{"rule dative plural", "Соответствия правилам детально раскрыты выше.", "правилам детально раскрыты выше"},
		{"rule instrumental singular", "Порядок изменён правилом выше.", "правилом выше"},
		{"rule instrumental plural", "Пользуйся правилами выше.", "правилами выше"},
		{"rule prepositional singular", "Сведения в правиле выше точнее.", "в правиле выше"},
		{"rule prepositional plural", "Замечания по правилам ниже.", "по правилам ниже"},
		{"previous rule", "Сверь маршрут предыдущим правилом", "предыдущим правилом"},
		{"next rule", "Изменения внесены в следующем правиле", "в следующем правиле"},
		{"steps below", "Рабочие шаги перечислены ниже.", "шаги перечислены ниже"},
		{"next step means work step", "Перейди к следующему шагу задания", ""},
		{"previous step means work step", "Вспомни предыдущий шаг операции", ""},
		{"threshold above", "Запас держи выше порога.", ""},
		{"limit below", "Расход останется ниже предела.", ""},
		{"above before unit", "Решение выше допустимого уровня строки", ""},
		{"below before unit", "Ниже правило о хранении листов", ""},
		{"immediate above before unit", "Выше пункт нельзя ставить в списке", ""},
		{"immediate below before unit", "Ниже строка перечисляет исключения", ""},
		{"punctuation ends clause", "Пункт; ниже перечислены детали", ""},
		{"parenthesis ends clause", "Строка); причина указана выше", ""},
		{"dash ends clause", "Раздел — ниже приведена схема", ""},
		{"different lines do not join", "Следующая\nзадача; запись стоит первым пунктом", ""},
		{"previous adjective must be adjacent", "Предыдущим из списка пунктом", ""},
		{"quoted phrase is opaque", "Цитируй «по строке выше» без толкования", ""},
		{"backtick phrase is opaque", "Маркер `следующей строкой` оставь буквально", ""},
		{"low-high quote pair is opaque", "Запись „пункт выше“ сохрани как цитату", ""},
		{"unclosed quote does not hide later text", "Незаконченная ссылка « и строка выше важна", "строка выше"},
		{"lone close quote does not hide text", "Знак » и строка выше отмечены", "строка выше"},
		{"unknown quote does not hide text", "Символ ‘ перед строкой выше не открывает цитату", "строкой выше"},
		{"named section below", "Раздел «Выкатка» ниже описывает запуск.", ""},
		{"named point above", "Сверь пункт `X` выше.", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := positionalReference(tc.text); got != tc.want {
				t.Fatalf("positionalReference(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

func TestPositionalReferenceInHeaderAndSummary(t *testing.T) {
	codex, _, frag := ruleBasisFixture(t, "Тестовая заметка о сортировке.\n", "цель: отсортировать записи", "основание: выдуманная схема", "снять_когда: условие")
	raw, err := os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("снять_когда: условие\n"), []byte("снять_когда: после проверки (строка выше)\n"), 1)
	mustWrite(t, frag, raw)
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := frag + ": ссылка по положению «строка выше» — назови цель по смыслу"
	if countWarningsContaining(warnings, want) != 1 {
		t.Fatalf("warnings = %v, want header warning %q", warnings, want)
	}
	if !strings.HasSuffix(RuleWarningSummary(warnings), "; ссылок по положению: 1") {
		t.Fatalf("summary = %q, want positional count", RuleWarningSummary(warnings))
	}
}

func TestPositionalReferenceChecksGoalAndRemovalButNotBasis(t *testing.T) {
	cases := []struct {
		name, field, value string
		want               bool
	}{
		{"goal", "цель", "Сверять по строке выше", true},
		{"removal condition", "снять_когда", "Удалить после пункта ниже", true},
		{"basis source", "основание", "источник цитирует девять строк выше", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			codex, _, frag := ruleBasisFixture(t, "Сохраняй порядок карточек в каталоге.\n", "цель: цель для тестового правила", "основание: источник схемы", "снять_когда: условие для проверки")
			raw, err := os.ReadFile(frag)
			if err != nil {
				t.Fatal(err)
			}
			old := []byte(tc.field + ": ")
			lineStart := bytes.Index(raw, old)
			if lineStart < 0 {
				t.Fatalf("field %s not found in fragment", tc.field)
			}
			lineEnd := bytes.IndexByte(raw[lineStart:], '\n') + lineStart
			updated := append(bytes.Clone(raw[:lineStart]), []byte(tc.field+": "+tc.value)...)
			updated = append(updated, raw[lineEnd:]...)
			mustWrite(t, frag, updated)
			warnings, err := CheckWithWarnings(codex, CheckOptions{})
			if err != nil {
				t.Fatal(err)
			}
			got := countWarningsContaining(warnings, frag+": ссылка по положению") == 1
			if got != tc.want {
				t.Fatalf("positional warning = %v, want %v; warnings = %v", got, tc.want, warnings)
			}
		})
	}
}
