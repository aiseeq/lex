package lex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A reference through a symlinked directory checks the corpus file it points to.
func TestRefThroughSymlinkChecksCorpusFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	tree := filepath.Join(home, "tree")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tree, filepath.Join(home, "link")); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(tree, "guide.md")
	if err := os.WriteFile(target, []byte("# Бюджет\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(home, "source.md")
	corpus := textCorpus{
		target: []byte("# Бюджет\n"),
		source: []byte("Ссылка: раздел «Деньги» в `~/link/guide.md`.\nСсылка: раздел «Бюджет» в `" + filepath.Join(home, "link", "guide.md") + "`.\n"),
	}
	findings := findTextRefs(corpus, filepath.Join(home, "decisions.md"))
	var missing []string
	for _, finding := range findings {
		if finding.kind == 2 {
			missing = append(missing, finding.missing)
		}
	}
	if len(missing) != 1 || !strings.Contains(missing[0], "«Деньги»") {
		t.Fatalf("want one missing section «Деньги» through the symlink, got %v (all %v)", missing, findings)
	}
}
