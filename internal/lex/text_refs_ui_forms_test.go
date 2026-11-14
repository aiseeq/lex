package lex

import (
	"path/filepath"
	"testing"
)

// A section of a tab or panel named in the instrumental plural is a screen section, not a document section.
func TestTextRefsInterfacePluralForms(t *testing.T) {
	journal := filepath.Join(t.TempDir(), "decisions.md")
	for _, line := range []string{
		"Между вкладками раздела «Склад» можно переключаться.",
		"Под панелями раздела «Теплица» есть кнопка полива.",
	} {
		path := filepath.Join(t.TempDir(), "rules", "garden.md")
		findings := findTextRefs(textCorpus{path: []byte("## Сад\n" + line + "\n")}, journal)
		if len(findings) != 0 {
			t.Errorf("раздел интерфейса в %q стал ссылкой на раздел документа: %v", line, findings)
		}
	}
}
