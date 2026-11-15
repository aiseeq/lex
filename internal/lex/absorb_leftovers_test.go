package lex

import (
	"bytes"
	"os"
	"path/filepath"
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

func TestAbsorbEditNextToDeletedRuleIsNotRefused(t *testing.T) {
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

// With printed goals the goal line under a rule names its fragment: a deleted rule's goal
// under an edited neighbour keeps the deleted rule's fragment (its other fields stay with
// its goal), and no fragment takes over another fragment's goal.
func TestAbsorbGoalLineKeepsItsFragment(t *testing.T) {
	for _, tc := range []struct{ name, input, del, old, new string }{
		{"long rules", "- rule four text\n- rule five text\n", "- rule four text\n  Зачем: reason four\n", "- rule five text\n  Зачем: reason five\n", "- rule five edited\n  Зачем: reason four\n"},
		{"short rules", "- Ab\n- Cd\n", "- Ab\n  Зачем: reason four\n", "- Cd\n  Зачем: reason five\n", "- Cd!\n  Зачем: reason four\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codex, out, mp, m := goalDeleteFixture(t, tc.input, []string{"reason four", "reason five"})
			before := map[string]string{}
			for _, id := range m.parts {
				raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
				if err != nil {
					t.Fatal(err)
				}
				before[id] = purposeValue(raw)
			}
			built, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			edited := bytes.Replace(built, []byte(tc.del), nil, 1)
			edited = bytes.Replace(edited, []byte(tc.old), []byte(tc.new), 1)
			mustWrite(t, out, edited)
			if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
				t.Fatalf("Absorb: %v", err)
			}
			for _, id := range mustReadManifest(t, mp).parts {
				raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
				if err != nil {
					t.Fatal(err)
				}
				if goal := purposeValue(raw); goal != before[id] {
					t.Fatalf("fragment %s took goal %q, had %q", id, goal, before[id])
				}
			}
			assertAbsorbedOutputEquals(t, codex, out, edited)
		})
	}
}
