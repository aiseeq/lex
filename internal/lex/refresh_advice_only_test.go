package lex

import (
	"strings"
	"testing"
)

func TestRefreshSharedDropAdviceOnly(t *testing.T) {
	t.Run("two-pass advice omits the standalone command", func(t *testing.T) {
		root, codex, outputs, _ := sharedDropFixture(t, []string{"shared para"})
		mustWrite(t, outputs["a.md"], []byte("x\n"))

		err := ImportWithOptions(codex, outputs["a.md"], ImportOptions{Refresh: true, Home: root})
		if err == nil {
			t.Fatal("expected refresh refusal")
		}
		message := err.Error()
		if !strings.Contains(message, "first delete only the shared paragraph") {
			t.Fatalf("refresh refusal lacks two-pass advice: %v", err)
		}
		if strings.Contains(message, "run `lex absorb --codex") {
			t.Fatalf("refresh refusal includes the standalone absorb command: %v", err)
		}
	})

	t.Run("ordinary refusal keeps its command", func(t *testing.T) {
		root, codex, outputs, _ := sharedDropFixture(t, []string{"shared para"})
		mustWrite(t, outputs["a.md"], []byte("# A\n"))

		err := ImportWithOptions(codex, outputs["a.md"], ImportOptions{Refresh: true, Home: root})
		if err == nil {
			t.Fatal("expected refresh refusal")
		}
		message := err.Error()
		if strings.Contains(message, "first delete only") {
			t.Fatalf("ordinary refresh refusal unexpectedly has two-pass advice: %v", err)
		}
		if !strings.Contains(message, "run `lex absorb --codex ") {
			t.Fatalf("ordinary refresh refusal lost its absorb command: %v", err)
		}
	})
}
