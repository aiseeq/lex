package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func goalDeleteFixture(t *testing.T, input string, goals []string) (string, string, string, manifest) {
	t.Helper()
	codex, out, manifestPath, m := purposeFixture(t, input)
	purposeSet(t, codex, true)
	for i, goal := range goals {
		m = mustReadManifest(t, manifestPath)
		purposeFragment(t, codex, manifestPath, m, i, goal)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	return codex, out, manifestPath, mustReadManifest(t, manifestPath)
}

func goalDeleteAbsorb(t *testing.T, codex, out, manifestPath string, edit func([]byte) []byte) ([]byte, manifest) {
	t.Helper()
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	data = edit(data)
	mustWrite(t, out, data)
	if err := Absorb(codex, out, AbsorbOptions{DraftDir: codex}); err != nil {
		t.Fatal(err)
	}
	return data, mustReadManifest(t, manifestPath)
}

func assertGoalDeleteResult(t *testing.T, codex, out string, got []byte, m manifest, removed string, editedID, edited string) {
	t.Helper()
	for _, id := range m.parts {
		if id == removed {
			t.Fatalf("deleted fragment remains in manifest: %v", m.parts)
		}
	}
	if body := string(readFragmentBody(t, codex, editedID)); body != edited {
		t.Fatalf("edited body = %q, want %q", body, edited)
	}
	assertAbsorbedOutputEquals(t, codex, out, got)
}

func TestAbsorbDeletedRuleAndEditedRuleBelow(t *testing.T) {
	codex, out, manifestPath, m := goalDeleteFixture(t, "- rule three text\n- rule four text\n- rule five text\n", []string{"three", "four", "five"})
	removed, editedID := m.parts[1], m.parts[2]
	got, updated := goalDeleteAbsorb(t, codex, out, manifestPath, func(b []byte) []byte {
		b = bytes.Replace(b, []byte("- rule four text\n  Зачем: four\n"), nil, 1)
		return bytes.Replace(b, []byte("- rule five text"), []byte("- rule five text EDITED"), 1)
	})
	assertGoalDeleteResult(t, codex, out, got, updated, removed, editedID, "- rule five text EDITED\n")
}

func TestAbsorbTwoDeletedRulesAndEditedRuleBelow(t *testing.T) {
	codex, out, manifestPath, m := goalDeleteFixture(t, "- rule three text\n- rule four text\n- rule five text\n- rule six text\n", []string{"three", "four", "five", "six"})
	removed1, removed2, editedID := m.parts[1], m.parts[2], m.parts[3]
	got, updated := goalDeleteAbsorb(t, codex, out, manifestPath, func(b []byte) []byte {
		b = bytes.Replace(b, []byte("- rule four text\n  Зачем: four\n- rule five text\n  Зачем: five\n"), nil, 1)
		return bytes.Replace(b, []byte("- rule six text"), []byte("- rule six text EDITED"), 1)
	})
	assertGoalDeleteResult(t, codex, out, got, updated, removed1, editedID, "- rule six text EDITED\n")
	for _, id := range updated.parts {
		if id == removed2 {
			t.Fatalf("second deleted fragment remains: %v", updated.parts)
		}
	}
}

func TestAbsorbEditedHeadingAndDeletedRuleBelow(t *testing.T) {
	codex, out, manifestPath, m := goalDeleteFixture(t, "# Heading\n- rule two\n", []string{"", "two"})
	removed, editedID := m.parts[1], m.parts[0]
	got, updated := goalDeleteAbsorb(t, codex, out, manifestPath, func(b []byte) []byte {
		b = bytes.Replace(b, []byte("# Heading"), []byte("# Heading edited"), 1)
		return bytes.Replace(b, []byte("- rule two\n  Зачем: two\n"), nil, 1)
	})
	assertGoalDeleteResult(t, codex, out, got, updated, removed, editedID, "# Heading edited\n")
}

func TestAbsorbPurposeRefusalNamesLineToRestore(t *testing.T) {
	codex, out, _, _ := goalDeleteFixture(t, "- rule one\n- rule two\n", []string{"one", "two"})
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	b = bytes.Replace(b, []byte("  Зачем: one\n"), nil, 1)
	mustWrite(t, out, b)
	err = Absorb(codex, out, AbsorbOptions{DraftDir: codex})
	if err == nil || !strings.Contains(err.Error(), "missing or edited цель") || !strings.Contains(err.Error(), `"  Зачем: one"`) || strings.Contains(err.Error(), "run lex build") {
		t.Fatalf("unexpected refusal: %v", err)
	}
}

func TestAbsorbEditedHeadingAndDeletedOnlyRuleKeepsBlankLine(t *testing.T) {
	codex, out, manifestPath, m := goalDeleteFixture(t, "## Alpha\n- rule one text\n\n## Beta\n", []string{"", "reason one", ""})
	headingID, ruleID := m.parts[0], m.parts[1]
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("## Alpha\n"), []byte("## Alpha EDITED\n"), 1)
	data = bytes.Replace(data, []byte("- rule one text\n  Зачем: reason one\n"), nil, 1)
	mustWrite(t, out, data)
	if err := Absorb(codex, out, AbsorbOptions{DraftDir: codex}); err != nil {
		t.Fatal(err)
	}
	updated := mustReadManifest(t, manifestPath)
	if !slices.Contains(updated.parts, ruleID) {
		t.Fatalf("rule fragment was removed: %v", updated.parts)
	}
	if got := string(readFragmentBody(t, codex, headingID)); got != "## Alpha EDITED\n" {
		t.Fatalf("heading body = %q", got)
	}
	if got := string(readFragmentBody(t, codex, ruleID)); got != "\n" {
		t.Fatalf("rule fragment body = %q, want one blank line", got)
	}
	assertAbsorbedOutputEquals(t, codex, out, data)
}

func TestAbsorbUnexpectedPurposeRefusalNamesLine(t *testing.T) {
	codex, out, _, _ := goalDeleteFixture(t, "- rule one\n- rule two\n", []string{"one", "two"})
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	const added = "  Зачем: custom line\n"
	mustWrite(t, out, append(data, []byte(added)...))
	err = Absorb(codex, out, AbsorbOptions{DraftDir: codex})
	if err == nil || !strings.Contains(err.Error(), "unexpected or edited цель") || !strings.Contains(err.Error(), `"  Зачем: custom line"`) || strings.Contains(err.Error(), "run lex build") {
		t.Fatalf("unexpected refusal: %v", err)
	}
}

func TestAbsorbMovedPurposeRefusalNamesLine(t *testing.T) {
	codex, out, _, _ := goalDeleteFixture(t, "- rule one\n- rule two\n", []string{"", "reason"})
	before := treeBytes(t, codex)
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("- rule one\n- rule two\n  Зачем: reason\n"), []byte("- rule one\n  Зачем: reason\n- rule two\n"), 1)
	mustWrite(t, out, data)
	err = Absorb(codex, out, AbsorbOptions{DraftDir: codex})
	if err == nil || !strings.Contains(err.Error(), "цель moved to another fragment") || !strings.Contains(err.Error(), `"  Зачем: reason"`) || strings.Contains(err.Error(), "run lex build") {
		t.Fatalf("unexpected refusal: %v", err)
	}
	if after := treeBytes(t, codex); !bytes.Equal(before, after) {
		t.Fatal("refused absorb changed codex")
	}
}

// refuseGoalDelete applies edit to the built file and expects absorb to refuse
// with want in the message, leaving fragments and the manifest untouched.
func refuseGoalDelete(t *testing.T, codex, out, manifestPath string, edit func([]byte) []byte, want string) {
	t.Helper()
	beforeFragments := treeBytes(t, filepath.Join(codex, "frag"))
	beforeManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, out, edit(data))
	err = Absorb(codex, out, AbsorbOptions{DraftDir: codex})
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("absorb error = %v, want %q", err, want)
	}
	if after := treeBytes(t, filepath.Join(codex, "frag")); !bytes.Equal(beforeFragments, after) {
		t.Fatal("refused absorb changed fragments")
	}
	afterManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeManifest, afterManifest) {
		t.Fatal("refused absorb changed manifest")
	}
}

func TestAbsorbRefusesEditsOnBothSidesOfDeletedRule(t *testing.T) {
	codex, out, manifestPath, _ := goalDeleteFixture(t, "## Alpha\n- rule one text\n## Beta\n", []string{"", "reason one", ""})
	refuseGoalDelete(t, codex, out, manifestPath, func(data []byte) []byte {
		data = bytes.Replace(data, []byte("## Alpha\n"), []byte("## Alpha EDITED\n"), 1)
		data = bytes.Replace(data, []byte("- rule one text\n  Зачем: reason one\n"), nil, 1)
		return bytes.Replace(data, []byte("## Beta\n"), []byte("## Beta EDITED\n"), 1)
	}, "missing or edited цель")
}

func TestAbsorbRefusesEditAcrossTwoFragmentsNextToDeletedRule(t *testing.T) {
	codex, out, manifestPath, m := goalDeleteFixture(t, "# Alpha\n## Beta\n- rule one text\n- rule two text\n", []string{"", "", "reason one", "reason two"})
	if len(m.parts) != 4 {
		t.Fatalf("fixture parts = %v, want two headings and two rules", m.parts)
	}
	refuseGoalDelete(t, codex, out, manifestPath, func(data []byte) []byte {
		return bytes.Replace(data, []byte("# Alpha\n## Beta\n- rule one text\n  Зачем: reason one\n"), []byte("# Gamma\n"), 1)
	}, "missing or edited цель")
}

func TestAbsorbRefusesRuleReplacedTogetherWithPurposeLine(t *testing.T) {
	codex, out, manifestPath, _ := goalDeleteFixture(t, "- rule one\n- rule two\n", []string{"one", "two"})
	refuseGoalDelete(t, codex, out, manifestPath, func(data []byte) []byte {
		return bytes.Replace(data, []byte("- rule one\n  Зачем: one\n"), []byte("- replacement\n"), 1)
	}, "missing or edited цель")
}

func TestAbsorbDeletedRuleAndRuleBelowRewrittenInTwoLines(t *testing.T) {
	codex, out, manifestPath, m := goalDeleteFixture(t, "- rule four text\n- rule five text\n", []string{"reason four", "reason five"})
	deleted, edited := m.parts[0], m.parts[1]
	got, updated := goalDeleteAbsorb(t, codex, out, manifestPath, func(data []byte) []byte {
		data = bytes.Replace(data, []byte("- rule four text\n  Зачем: reason four\n"), nil, 1)
		return bytes.Replace(data, []byte("- rule five text"), []byte("- rule five edited\n  continued line"), 1)
	})
	assertGoalDeleteResult(t, codex, out, got, updated, deleted, edited, "- rule five edited\n  continued line\n")
}

func TestAbsorbPurposeRefusalNamesEveryMissingLineOfHunk(t *testing.T) {
	codex, out, manifestPath, _ := goalDeleteFixture(t, "- rule four text\n- rule five text\n", []string{"reason four", "reason five"})
	beforeFragments := treeBytes(t, filepath.Join(codex, "frag"))
	beforeManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("- rule four text\n  Зачем: reason four\n"), nil, 1)
	data = bytes.Replace(data, []byte("- rule five text\n"), []byte("- rule five text EDITED\n"), 1)
	data = bytes.Replace(data, []byte("  Зачем: reason five\n"), nil, 1)
	mustWrite(t, out, data)
	err = Absorb(codex, out, AbsorbOptions{DraftDir: codex})
	if err == nil || !strings.Contains(err.Error(), "missing or edited цель") || !strings.Contains(err.Error(), `"  Зачем: reason four"`) || !strings.Contains(err.Error(), `"  Зачем: reason five"`) || strings.Contains(err.Error(), "run lex build") {
		t.Fatalf("unexpected refusal: %v", err)
	}
	if after := treeBytes(t, filepath.Join(codex, "frag")); !bytes.Equal(beforeFragments, after) {
		t.Fatal("refused absorb changed fragments")
	}
	afterManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeManifest, afterManifest) {
		t.Fatal("refused absorb changed manifest")
	}
}

func TestAbsorbRefusesEditedRuleWithoutPurposeLineNextToEditedRule(t *testing.T) {
	codex, out, manifestPath, _ := goalDeleteFixture(t, "- rule four text\n- rule five text\n", []string{"reason four", "reason five"})
	refuseGoalDelete(t, codex, out, manifestPath, func(data []byte) []byte {
		data = bytes.Replace(data, []byte("- rule four text\n"), []byte("- rule four text typo\n"), 1)
		data = bytes.Replace(data, []byte("  Зачем: reason four\n"), nil, 1)
		return bytes.Replace(data, []byte("- rule five text"), []byte("- rule five text EDITED"), 1)
	}, "missing or edited цель")
}

func TestAbsorbRefusesEditBelowDeletedHeadingAndRule(t *testing.T) {
	codex, out, manifestPath, _ := goalDeleteFixture(t, "## Alpha\n- rule one text\n## Beta\n", []string{"", "reason one", ""})
	refuseGoalDelete(t, codex, out, manifestPath, func(data []byte) []byte {
		data = bytes.Replace(data, []byte("## Alpha\n- rule one text\n  Зачем: reason one\n"), nil, 1)
		return bytes.Replace(data, []byte("## Beta\n"), []byte("## Beta EDITED\n"), 1)
	}, "missing or edited цель")
}

func TestAbsorbPurposeRefusalNamesOnlyLinesOfFirstHunk(t *testing.T) {
	codex, out, manifestPath, _ := goalDeleteFixture(t, "- rule one text\n- rule two text\n- rule three text\n", []string{"reason one", "reason two", "reason three"})
	refuseGoalDelete(t, codex, out, manifestPath, func(data []byte) []byte {
		data = bytes.Replace(data, []byte("- rule one text\n  Зачем: reason one\n"), []byte("- rule one text typo\n"), 1)
		return bytes.Replace(data, []byte("  Зачем: reason three\n"), nil, 1)
	}, `put the line "  Зачем: reason one" back`)
}
