package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Мера absorb-move-with-edit-empty-purpose: перенос правила и правка соседа одним absorb.
// Верно одно из двух: absorb отказывает и кодекс не меняется, или каждое правило сохраняет свою цель.
func TestAbsorbMoveWithNeighborEdit(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(built []byte) []byte
	}{
		{"move third to top, edit second", func(b []byte) []byte {
			b = append([]byte("- third\n\n"), bytes.Replace(b, []byte("\n\n- third\n"), []byte("\n"), 1)...)
			return bytes.Replace(b, []byte("- second\n"), []byte("- second edited\n"), 1)
		}},
		{"move first to bottom, edit third", func(b []byte) []byte {
			b = append(bytes.Replace(b, []byte("- first\n\n"), nil, 1), []byte("\n- first\n")...)
			return bytes.Replace(b, []byte("- third\n"), []byte("- third edited\n"), 1)
		}},
		{"move third to top, edit first", func(b []byte) []byte {
			b = append([]byte("- third\n\n"), bytes.Replace(b, []byte("\n\n- third\n"), []byte("\n"), 1)...)
			return bytes.Replace(b, []byte("- first\n"), []byte("- first edited\n"), 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codex, out, mp, _ := moveFixture(t, false, false)
			frags := snapshotMoveEditFrags(t, codex)
			built, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			mustWrite(t, out, tc.edit(built))
			if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
				if after := snapshotMoveEditFrags(t, codex); after != frags {
					t.Fatalf("отказ (%v), но кодекс изменён", err)
				}
				return
			}
			goals := map[string]string{}
			for _, id := range mustReadManifest(t, mp).parts {
				raw, _ := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
				body := string(readFragmentBody(t, codex, id))
				for _, rule := range []string{"first", "second", "third"} {
					if strings.Contains(body, "- "+rule) {
						goals[rule] = purposeValue(raw)
					}
				}
			}
			for _, rule := range []string{"first", "second", "third"} {
				if goals[rule] != "reason "+rule {
					t.Fatalf("правило %s потеряло цель: %q (все: %v)", rule, goals[rule], goals)
				}
			}
		})
	}
}

func snapshotMoveEditFrags(t *testing.T, codex string) string {
	t.Helper()
	var b strings.Builder
	_ = filepath.WalkDir(filepath.Join(codex, "frag"), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			data, _ := os.ReadFile(path)
			b.WriteString(path + "\n" + string(data) + "\n")
		}
		return nil
	})
	return b.String()
}

func TestAbsorbMoveWithNeighborDeletion(t *testing.T) {
	codex, out, mp, m := moveFixture(t, false, false)
	before, err := snapshotCodexFiles(codex)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("- third\n\n- first\n")
	mustWrite(t, out, want)
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		after, snapshotErr := snapshotCodexFiles(codex)
		if snapshotErr != nil {
			t.Fatal(snapshotErr)
		}
		if len(changedCodexFiles(before, after)) != 0 {
			t.Fatalf("refusal changed codex: %v", err)
		}
		if !strings.Contains(err.Error(), "first") || !strings.Contains(err.Error(), "then") {
			t.Fatalf("no two-pass advice: %v", err)
		}
		return
	}
	updated := mustReadManifest(t, mp)
	for _, pair := range []struct{ rule, goal, id string }{{"first", "reason first", m.parts[0]}, {"third", "reason third", m.parts[2]}} {
		found := false
		for _, id := range updated.parts {
			if strings.Contains(string(readFragmentBody(t, codex, id)), "- "+pair.rule+"\n") {
				raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
				if err != nil {
					t.Fatal(err)
				}
				if purposeValue(raw) != pair.goal {
					t.Fatalf("%s goal = %q", pair.rule, purposeValue(raw))
				}
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %s", pair.rule)
		}
	}
	assertAbsorbedOutputEquals(t, codex, out, want)
}

func TestAbsorbMoveSplitRuleKeepsPurposeOnTheRuleLine(t *testing.T) {
	input := "# Guide\n\n## Tasks\n- rule one text\n  more about one\n- rule two text\n  more about two\n- rule three text\n  more about three\n- rule four text\n  more about four\n\nClosing words.\n"
	codex, out, mp, m := purposeFixture(t, input)
	goals := make([]string, len(m.parts))
	for i, id := range m.parts {
		if strings.Contains(string(readFragmentBody(t, codex, id)), "rule two") {
			goals[i] = "reason two"
		}
	}
	setPurposeGoals(t, codex, mp, m, goals...)
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	edited := "# Guide\n\n## Tasks\n- rule two text\n- rule one text\n  more about one\n\n  more about two\n- rule three text\n  more about three\r\n- rule four text\n- added 55 text\n  more about four\n\nClosing words.\n"
	mustWrite(t, out, []byte(edited))
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		t.Fatal(err)
	}
	updated := mustReadManifest(t, mp)
	// The rule line keeps its goal; the continuation split off by a blank line is a new
	// fragment without one, and no fragment carries a copy of another's goal.
	for _, tc := range []struct{ line, goal string }{{"- rule two text", "reason two"}, {"  more about two", ""}} {
		line := tc.line
		found := false
		for _, id := range updated.parts {
			if strings.Contains(string(readFragmentBody(t, codex, id)), line) {
				raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
				if err != nil {
					t.Fatal(err)
				}
				if got := purposeValue(raw); got != tc.goal {
					t.Fatalf("%q goal = %q", line, got)
				}
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %q", line)
		}
	}
	assertAbsorbedOutputEquals(t, codex, out, []byte(edited))
}

// With printed goals the goal line names its fragment: the fragment of "reason third"
// follows that line under the inserted rule, and no fragment takes another's goal.
func TestAbsorbPrintedPurposeFollowsItsGoalLine(t *testing.T) {
	codex, out, mp, m := moveFixture(t, true, false)
	goals := map[string]string{}
	for _, id := range m.parts {
		raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
		if err != nil {
			t.Fatal(err)
		}
		goals[id] = purposeValue(raw)
	}
	edited := "- first\n  Зачем: reason first\n\n- third\n- second\n  Зачем: reason second\n\n- added\n  Зачем: reason third\n"
	mustWrite(t, out, []byte(edited))
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		t.Fatalf("Absorb: %v", err)
	}
	for _, id := range mustReadManifest(t, mp).parts {
		raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
		if err != nil {
			t.Fatal(err)
		}
		if goal, had := purposeValue(raw), goals[id]; goal != "" && goal != had {
			t.Fatalf("fragment %s took goal %q, had %q", id, goal, had)
		}
	}
	assertAbsorbedOutputEquals(t, codex, out, []byte(edited))
}

// Rules whose line occurs more than once keep their goals: their occurrences are matched to
// the old ones in order, or absorb refuses without changing the codex.
func TestAbsorbMoveKeepsGoalsOfRepeatedRules(t *testing.T) {
	for _, tc := range []struct {
		name, input, edited string
		goals               []string
		keep                map[string][]string // line -> goals of the fragments holding it
	}{
		{"move a rule above repeated ones and edit a neighbour",
			"# Guide\n\n## Tasks\n- Do it\n- Beta rule\n- Do it\n- Gamma rule\n",
			"# Guide\n\n## Tasks\n- Gamma rule\n- Do it\n- Beta rule!\n- Do it\n",
			[]string{"", "", "ga", "gb", "gc", "gd"},
			map[string][]string{"- Do it": {"ga", "gc"}, "- Gamma rule": {"gd"}}},
		{"move a rule past a repeated one",
			"# Guide\n\n## Tasks\n- same rule\n- other rule\n- same rule\n",
			"# Guide\n\n## Tasks\n- same rule\n- same rule\n- other rule\n",
			[]string{"", "", "ga", "gc", "gb"},
			map[string][]string{"- same rule": {"ga", "gb"}, "- other rule": {"gc"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codex, out, mp, m := purposeFixture(t, tc.input)
			if len(m.parts) != len(tc.goals) {
				t.Fatalf("parts = %v", m.parts)
			}
			setPurposeGoals(t, codex, mp, m, tc.goals...)
			if err := Build(codex, false); err != nil {
				t.Fatal(err)
			}
			frags := snapshotMoveEditFrags(t, codex)
			mustWrite(t, out, []byte(tc.edited))
			if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
				if snapshotMoveEditFrags(t, codex) != frags {
					t.Fatalf("refusal (%v) changed the codex", err)
				}
				return
			}
			got := map[string][]string{}
			for _, id := range mustReadManifest(t, mp).parts {
				raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
				if err != nil {
					t.Fatal(err)
				}
				for line := range tc.keep {
					if strings.Contains(string(readFragmentBody(t, codex, id)), line+"\n") {
						got[line] = append(got[line], purposeValue(raw))
					}
				}
			}
			for line, want := range tc.keep {
				g := append([]string(nil), got[line]...)
				slices.Sort(g)
				if !slices.Equal(g, want) {
					t.Fatalf("%q goals = %v, want %v", line, got[line], want)
				}
			}
			assertAbsorbedOutputEquals(t, codex, out, []byte(tc.edited))
		})
	}
}
