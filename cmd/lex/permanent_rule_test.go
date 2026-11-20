package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiseeq/lex/internal/lex"
)

func TestCheckCountsPermanentRulesWithoutPrintingThem(t *testing.T) {
	root := t.TempDir()
	codex, output := filepath.Join(root, "codex"), filepath.Join(root, "rules.md")
	body := "Правило о хранении ящиков.\n\nПравило о поливе теплицы.\n\nПравило о маркировке грядок.\n"
	if err := os.WriteFile(output, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	if err := lex.ImportWithOptions(codex, output, lex.ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	removals := map[string]string{
		"ящиков":  "не снимается — ящики считает кладовщик",
		"теплицы": "не снимается",
		"грядок":  "не снимается, пока грядки без табличек",
	}
	frags, err := filepath.Glob(filepath.Join(codex, "frag", "*", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	filled := 0
	for _, frag := range frags {
		raw, err := os.ReadFile(frag)
		if err != nil {
			t.Fatal(err)
		}
		for word, removal := range removals {
			if !bytes.Contains(raw, []byte(word)) {
				continue
			}
			raw = bytes.Replace(raw, []byte("цель: \n"), []byte("цель: склад в порядке\n"), 1)
			raw = bytes.Replace(raw, []byte("основание: \n"), []byte("основание: журнал склада\n"), 1)
			raw = bytes.Replace(raw, []byte("снять_когда: \n"), []byte("снять_когда: "+removal+"\n"), 1)
			if err := os.WriteFile(frag, raw, 0644); err != nil {
				t.Fatal(err)
			}
			filled++
		}
	}
	if filled != len(removals) {
		t.Fatalf("filled %d fragments, want %d", filled, len(removals))
	}
	if err := lex.Build(codex, false); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := captureStreams(t, func() error { return run([]string{"check", "--codex", codex}) })
	if err != nil {
		t.Fatalf("check failed: %v\n%s", err, stderr)
	}
	if strings.Contains(stderr, ": постоянное правило") {
		t.Fatalf("stderr prints the summary-only note: %q", stderr)
	}
	for _, want := range []string{
		"«не снимается» без причины",
		"«не снимается, пока X» — это условие снятия",
		"; постоянных правил: 2, из них без причины: 1, с общей причиной: 0; «не снимается, пока»: 1; пустых фрагментов: 0; пустых разделителей: 0\n",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, want %q", stderr, want)
		}
	}
	if got := strings.Count(stderr, "warning: "); got != 2 {
		t.Fatalf("stderr = %q, want 2 warnings", stderr)
	}
}
