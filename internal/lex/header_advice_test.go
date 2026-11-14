package lex

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusRefusesTextAboveHeader(t *testing.T) {
	_, out, codex, _, data := builtWithHeader(t, "# A\n\n- one\n")
	mustWrite(t, out, append([]byte("intro\n"), data...))
	status, err := Status(codex)
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != 1 || !strings.Contains(status[0].Refusal, "text above lex header (found at line 2)") || status[0].Command != "" || status[0].Problem != "" {
		t.Fatalf("status = %#v", status)
	}
}

func TestAdviceForeignHeaderAboveOwnHeader(t *testing.T) {
	dir, out, codex, _, data := builtWithHeader(t, "# A\n\n- one\n")
	mustWrite(t, out, append(generatedMarkerForCodex("x", filepath.Join(dir, "other")), data...))
	check, err := CheckWithWarnings(codex, CheckOptions{})
	if err == nil || !strings.Contains(err.Error(), "the file carries") || strings.Contains(err.Error(), "text above lex header") {
		t.Fatalf("check advice = %q, error = %v", check, err)
	}
	status, err := Status(codex)
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != 1 || status[0].ForeignCodex == "" || status[0].Refusal != "" {
		t.Fatalf("status = %#v", status)
	}
}
