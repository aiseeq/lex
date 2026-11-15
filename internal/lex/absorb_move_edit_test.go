package lex

import (
	"bytes"
	"os"
	"path/filepath"
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

func TestAbsorbMoveSplitRuleKeepsPurpose(t *testing.T) {
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
	for _, line := range []string{"- rule two text", "  more about two"} {
		found := false
		for _, id := range updated.parts {
			if strings.Contains(string(readFragmentBody(t, codex, id)), line) {
				raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
				if err != nil {
					t.Fatal(err)
				}
				if got := purposeValue(raw); got != "reason two" {
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

func TestAbsorbRejectsMovedPrintedPurposeBesideInsertedRule(t *testing.T) {
	codex, out, _, _ := moveFixture(t, true, false)
	before, err := snapshotCodexFiles(codex)
	if err != nil {
		t.Fatal(err)
	}
	edited := "- first\n  Зачем: reason first\n\n- third\n- second\n  Зачем: reason second\n\n- added\n  Зачем: reason third\n"
	mustWrite(t, out, []byte(edited))
	if err := Absorb(codex, out, AbsorbOptions{}); err == nil {
		t.Fatal("accepted a purpose moved away from its rule")
	}
	after, err := snapshotCodexFiles(codex)
	if err != nil {
		t.Fatal(err)
	}
	if changed := changedCodexFiles(before, after); len(changed) != 0 {
		t.Fatalf("refusal changed codex: %v", changed)
	}
}
