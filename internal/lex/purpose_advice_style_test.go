package lex

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// A deleted rule whose text is the start of a neighbour rule that was edited
// longer: the edited line belongs to the neighbour (longest matching start),
// so the orphan goal has no rule text rather than having moved.
func TestAbsorbPurposeEditedNeighbourWithDeletedRulePrefix(t *testing.T) {
	input := "- rule one\n- delete files\n- delete files carefully\n- rule four\n"
	codex, out, _, _ := goalDeleteFixture(t, input, []string{"g1", "g2", "g3", "g4"})
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	edited := bytes.Replace(data, []byte("- delete files\n"), nil, 1)
	edited = bytes.Replace(edited, []byte("- delete files carefully\n"), []byte("- delete files carefully now\n"), 1)
	mustWrite(t, out, edited)
	err = Absorb(codex, out, AbsorbOptions{DraftDir: codex})
	if err == nil || !strings.Contains(err.Error(), "has no rule text") || !strings.Contains(err.Error(), `"  Зачем: g2"`) {
		t.Fatalf("absorb = %v, want the g2 goal line named as having no rule text", err)
	}
}

// The CRLF variant of the moved-goal advice starts with the fragment id, like
// every other absorb refusal.
func TestAbsorbPurposeCRLFMovedAdviceStartsWithID(t *testing.T) {
	codex, out, _, m := goalDeleteFixture(t, "- rule one\n- rule two\n- rule three\n- rule four\n", []string{"g1", "g2", "g3", "g4"})
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	edited := bytes.Replace(data, []byte("  Зачем: g3\n"), nil, 1)
	edited = bytes.Replace(edited, []byte("  Зачем: g1\n"), []byte("  Зачем: g1\n  Зачем: g3\r\n"), 1)
	mustWrite(t, out, edited)
	err = Absorb(codex, out, AbsorbOptions{DraftDir: codex})
	if err == nil {
		t.Fatal("absorb accepted a moved CRLF goal line")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, m.parts[2]+": ") {
		t.Fatalf("advice = %q, want it to start with fragment id %q", msg, m.parts[2])
	}
	if !strings.Contains(msg, "CRLF") || !strings.Contains(msg, "moved to another fragment") {
		t.Fatalf("advice = %q, want both the CRLF and the moved cause", err.Error())
	}
}
