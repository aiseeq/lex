package lex

import (
	"strings"
	"testing"
)

func adviceOrderingCatalog() *manifestCatalog {
	paths := []string{"manifest-selected", "manifest-z", "manifest-b", "manifest-a"}
	catalog := newManifestCatalog(paths)
	for i, output := range []string{"selected.md", "z.md", "b.md", "a.md"} {
		catalog.items[paths[i]] = manifest{out: output, parts: []string{"z-shared", "a-shared"}}
	}
	return catalog
}

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

func TestSharedDropAdviceSortsAndDeduplicates(t *testing.T) {
	t.Run("refresh", func(t *testing.T) {
		removed := []removedSharedFragment{
			{id: "z-id", outputs: []string{"z.md", "b.md", "a.md", "b.md"}},
			{id: "a-id", outputs: []string{"z.md", "a.md"}},
			{id: "z-id", outputs: []string{"b.md"}},
		}
		err := refreshSharedRemovalRefusal("selected.md", "codex", removed, true)
		if err == nil {
			t.Fatal("expected refresh refusal")
		}
		want := "first delete only the shared paragraphs `a-id`, `z-id` (still used by a.md, b.md, z.md)"
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refresh advice = %q, want sorted unique advice %q", err, want)
		}
		if got := strings.Count(err.Error(), "still used by a.md, b.md, z.md"); got != 2 {
			t.Fatalf("refresh shared-output lists occur %d times, want 2 (refusal and advice): %v", got, err)
		}
	})

	t.Run("absorb", func(t *testing.T) {
		advice, ok, err := sharedDropAdvice(
			"selected.md",
			[]string{"z-shared", "a-shared", "z-shared"},
			[][2]int{{0, 1}, {1, 2}, {2, 3}},
			[][]byte{[]byte("z paragraph\n"), []byte("a paragraph\n"), []byte("z paragraph\n")},
			nil,
			adviceOrderingCatalog(),
		)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatal("expected shared-drop advice")
		}
		want := "first delete only the shared paragraphs `a-shared`, `z-shared` (still used by a.md, b.md, z.md)"
		if !strings.Contains(advice, want) {
			t.Fatalf("absorb advice = %q, want sorted unique advice %q", advice, want)
		}
	})
}
