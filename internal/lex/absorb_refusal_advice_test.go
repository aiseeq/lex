package lex

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func refusalAdvice(t *testing.T, codex, out string, edit func([]byte) []byte) string {
	t.Helper()
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	mustWrite(t, out, edit(data))
	err = Absorb(codex, out, AbsorbOptions{DraftDir: codex})
	if err == nil {
		t.Fatal("Absorb succeeded, want refusal")
	}
	message := strings.ReplaceAll(err.Error(), out, "")
	message = strings.ReplaceAll(message, codex, "")
	return message
}

func TestAbsorbRefusalRuleTextDeleted(t *testing.T) {
	codex, out, _, _ := goalDeleteFixture(t, "- rule two text\n- rule three text\n- rule four text\n", []string{"reason two", "reason three", "reason four"})
	message := refusalAdvice(t, codex, out, func(data []byte) []byte {
		return bytes.Replace(data, []byte("- rule three text\n"), nil, 1)
	})
	if !strings.Contains(message, `"  Зачем: reason three"`) || !(strings.Contains(message, "delete") || strings.Contains(message, "remove") || strings.Contains(message, "restore")) {
		t.Fatalf("refusal advice = %q, want quoted purpose line and delete or restore guidance", message)
	}
}

func TestAbsorbRefusalCRLF(t *testing.T) {
	codex, out, _, _ := goalDeleteFixture(t, "- rule one\n- rule two\n", []string{"reason one", "reason two"})
	message := refusalAdvice(t, codex, out, func(data []byte) []byte {
		data = bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n"))
		return bytes.Replace(data, []byte("- rule one"), []byte("- edited rule one"), 1)
	})
	if !(strings.Contains(message, "CRLF") || strings.Contains(message, "line endings")) || strings.Contains(message, "Зачем:") {
		t.Fatalf("refusal advice = %q, want line ending guidance without purpose line list", message)
	}
}

func TestAbsorbRefusalNoFinalNewline(t *testing.T) {
	codex, out, _, _ := goalDeleteFixture(t, "- rule one\n- rule two\n", []string{"reason one", "reason two"})
	message := refusalAdvice(t, codex, out, func(data []byte) []byte {
		return bytes.TrimSuffix(data, []byte("\n"))
	})
	if !strings.Contains(message, "newline") || strings.Contains(message, "Зачем:") {
		t.Fatalf("refusal advice = %q, want newline guidance without purpose line", message)
	}
}

func TestAbsorbRefusalCRLFRuleLineOnly(t *testing.T) {
	codex, out, _, _ := goalDeleteFixture(t, "- rule one\n- rule four\n", []string{"reason one", "reason four"})
	message := refusalAdvice(t, codex, out, func(data []byte) []byte {
		data = bytes.Replace(data, []byte("- rule one\n"), []byte("- rule one\r\n"), 1)
		return bytes.Replace(data, []byte("  Зачем: reason four\n"), nil, 1)
	})
	if !strings.Contains(message, `"  Зачем: reason four"`) || strings.Contains(message, "CRLF") || strings.Contains(message, "line endings") || strings.Contains(message, "newline") {
		t.Fatalf("refusal advice = %q, want the missing purpose line without line ending advice", message)
	}
}

func TestAbsorbRefusalNoNewlineAfterRuleWithoutGoal(t *testing.T) {
	codex, out, _, _ := goalDeleteFixture(t, "- rule one\n- rule four\n- rule five\n", []string{"reason one", "reason four", ""})
	message := refusalAdvice(t, codex, out, func(data []byte) []byte {
		data = bytes.Replace(data, []byte("  Зачем: reason one\n"), nil, 1)
		return bytes.TrimSuffix(data, []byte("\n"))
	})
	if !strings.Contains(message, `"  Зачем: reason one"`) || strings.Contains(message, "CRLF") || strings.Contains(message, "line endings") || strings.Contains(message, "newline") {
		t.Fatalf("refusal advice = %q, want the missing purpose line without line ending advice", message)
	}
}
