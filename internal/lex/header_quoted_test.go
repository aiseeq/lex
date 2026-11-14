package lex

import (
	"bytes"
	"strings"
	"testing"
)

// TestAdviceDeletedHeaderQuotedBelow covers a file whose lex header line was deleted while a copy of
// it is quoted further down: the refusal must also name restoring the header, not only moving text.
func TestAdviceDeletedHeaderQuotedBelow(t *testing.T) {
	_, out, codex, _, data := builtWithHeader(t, "# A\n\n- one\n")
	header, body, found := bytes.Cut(data, []byte("\n"))
	if !found {
		t.Fatalf("built file has no header line: %q", data)
	}
	edited := append(append(append([]byte(nil), body...), header...), '\n')
	mustWrite(t, out, edited)
	const want = "if the header line was deleted, restore it as the first line"
	if err := Absorb(codex, out, AbsorbOptions{}); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("absorb error = %v, want it to contain %q", err, want)
	}
	check, err := CheckWithWarnings(codex, CheckOptions{})
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("check advice = %q, error = %v", check, err)
	}
	status, err := Status(codex)
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != 1 || !strings.Contains(status[0].Refusal, want) {
		t.Fatalf("status = %#v", status)
	}
}
