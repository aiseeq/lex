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

func TestAbsorbPurposeRefusalNamesMovedGoal(t *testing.T) {
	codex, out, _, _ := goalDeleteFixture(t, "- rule four text\n- rule five text\n", []string{"reason four", "reason five"})
	built, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	edited := bytes.Replace(built, []byte("- rule four text\n  Зачем: reason four\n"), nil, 1)
	edited = bytes.Replace(edited, []byte("- rule five text\n"), []byte("- rule five edited\n"), 1)
	edited = bytes.Replace(edited, []byte("  Зачем: reason five\n"), []byte("  Зачем: reason four\n  Зачем: reason five\n"), 1)
	mustWrite(t, out, edited)
	err = Absorb(codex, out, AbsorbOptions{DraftDir: codex})
	if err == nil || !strings.Contains(err.Error(), `"  Зачем: reason four"`) || strings.Contains(err.Error(), `"  Зачем: reason five"`) {
		t.Fatalf("refusal = %v; want it to name the moved reason four line", err)
	}
}
