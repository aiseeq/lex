package lex

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestAbsorbPurposeEditAndInsertionBelowPurpose(t *testing.T) {
	codex, out, _, _ := goalDeleteFixture(t, "- rule one text\n- rule two text\n", []string{"reason one", "reason two"})
	built, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Replace(built, []byte("- rule one text\n  Зачем: reason one\n"), []byte("- rule one text edited\n  Зачем: reason one\n- inserted rule\n"), 1)
	if bytes.Equal(want, built) {
		t.Fatal("fixture did not contain the first printed purpose")
	}
	mustWrite(t, out, want)
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		t.Fatalf("Absorb refused an edited rule with a new rule below its purpose: %v", err)
	}
	assertAbsorbedOutputEquals(t, codex, out, want)
}

func TestAbsorbRefusesPurposeReassignedAfterRuleDeletion(t *testing.T) {
	codex, out, _, _ := goalDeleteFixture(t, "- rule four text\n- rule five text\n", []string{"reason four", "reason five"})
	built, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	edited := bytes.Replace(built, []byte("- rule four text\n  Зачем: reason four\n"), nil, 1)
	edited = bytes.Replace(edited, []byte("- rule five text\n"), []byte("- rule five edited\n"), 1)
	edited = bytes.Replace(edited, []byte("  Зачем: reason five\n"), []byte("  Зачем: reason four\n"), 1)
	mustWrite(t, out, edited)
	before := treeBytes(t, codex)
	err = Absorb(codex, out, AbsorbOptions{DraftDir: codex})
	if err == nil || !strings.Contains(err.Error(), "цель moved to another fragment") {
		t.Fatalf("Absorb refusal = %v; want a moved goal refusal", err)
	}
	if after := treeBytes(t, codex); !bytes.Equal(before, after) {
		t.Fatal("refused absorb changed the codex")
	}
}

func TestAbsorbGoalSimilarityDoesNotRefuseLegitimateEdits(t *testing.T) {
	type replacement struct{ old, new string }
	cases := []struct {
		name, input string
		goals       []string
		edits       []replacement
	}{
		{"rewrite-B-like-deleted-A", "- Всегда делай резервную копию\n- Не пиши в прод\n", []string{"цель a", "цель b"}, []replacement{
			{"- Всегда делай резервную копию\n  Зачем: цель a\n", ""},
			{"- Не пиши в прод\n", "- Всегда спрашивай перед записью в прод\n"},
		}},
		{"rewrite-A-like-deleted-B", "- Не пиши в прод\n- Всегда делай резервную копию\n", []string{"цель a", "цель b"}, []replacement{
			{"- Всегда делай резервную копию\n  Зачем: цель b\n", ""},
			{"- Не пиши в прод\n", "- Всегда спрашивай перед записью в прод\n"},
		}},
		{"edit-two-neighbours-same-start", "- Не используй табы\n- Не используй пробелы в конце\n", []string{"цель a", "цель b"}, []replacement{
			{"- Не используй табы\n", "- Не используй пробелы для отступа\n"},
			{"- Не используй пробелы в конце\n", "- Не оставляй пробелы в конце\n"},
		}},
		{"typo-fix-start", "- Проверяй тесты перед коммитом\n- Провряй линтер перед коммитом\n", []string{"цель a", "цель b"}, []replacement{
			{"- Проверяй тесты перед коммитом\n  Зачем: цель a\n", ""},
			{"- Провряй линтер", "- Проверяй линтер"},
		}},
		{"three-del-middle-edit-last", "- Правило один\n- Правило два\n- Правило три\n", []string{"цель 1", "цель 2", "цель 3"}, []replacement{
			{"- Правило два\n  Зачем: цель 2\n", ""},
			{"- Правило три\n", "- Правило два-три\n"},
		}},
		{"ascii-edit-after-delete", "- Use tabs for indentation\n- Use spaces in YAML\n", []string{"a", "b"}, []replacement{
			{"- Use tabs for indentation\n  Зачем: a\n", ""},
			{"- Use spaces in YAML\n", "- Use tabs in Makefiles, spaces in YAML\n"},
		}},
		{"rewrite-A-like-deleted-B-ascii", "- Keep it short\n- Prefer tables for comparisons\n", []string{"ga", "gb"}, []replacement{
			{"- Prefer tables for comparisons\n  Зачем: gb\n", ""},
			{"- Keep it short\n", "- Prefer short answers\n"},
		}},
		{"edit-two-ascii-no-delete", "- Run go test\n- Run go vet before push\n", []string{"ga", "gb"}, []replacement{
			{"- Run go test\n", "- Run go vet and go test\n"},
			{"- Run go vet before push\n", "- Run golangci-lint before push\n"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			codex, out, _, _ := goalDeleteFixture(t, tc.input, tc.goals)
			built, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			edited := built
			for _, change := range tc.edits {
				if !bytes.Contains(edited, []byte(change.old)) {
					t.Fatalf("fixture lacks %q in %q", change.old, edited)
				}
				edited = bytes.Replace(edited, []byte(change.old), []byte(change.new), 1)
			}
			mustWrite(t, out, edited)
			if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
				t.Fatalf("Absorb refused a legitimate edit: %v", err)
			}
			assertAbsorbedOutputEquals(t, codex, out, edited)
		})
	}
}

func TestAbsorbRefusesShortRuleGoalReassignment(t *testing.T) {
	codex, out, _, _ := goalDeleteFixture(t, "- Ab\n- Cd\n", []string{"ga", "gb"})
	built, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	edited := bytes.Replace(built, []byte("- Ab\n  Зачем: ga\n"), nil, 1)
	edited = bytes.Replace(edited, []byte("- Cd\n  Зачем: gb\n"), []byte("- Cd!\n  Зачем: ga\n"), 1)
	mustWrite(t, out, edited)
	before := treeBytes(t, codex)
	err = Absorb(codex, out, AbsorbOptions{DraftDir: codex})
	if err == nil || !strings.Contains(err.Error(), "цель moved to another fragment") {
		t.Fatalf("Absorb refusal = %v; want moved goal refusal", err)
	}
	if after := treeBytes(t, codex); !bytes.Equal(before, after) {
		t.Fatal("refused absorb changed the codex")
	}
}
