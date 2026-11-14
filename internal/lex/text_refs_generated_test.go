package lex

import (
	"path/filepath"
	"strings"
	"testing"
)

// A file lex built from fragments is checked for step and list numbering like any other text:
// the generated header does not hide a reference to a missing step or a gap in a list.
func TestNumberingCheckedInGeneratedFile(t *testing.T) {
	header := "<!-- lex: собран из ~/x/manifest/a.yaml. Правь фрагменты в ~/x/frag -->\n"
	for _, tc := range []struct {
		name, text, want string
	}{
		{"step reference", "Шаги 1 и 3 по саду", "нет шага 3"},
		{"list gap", "1) Первый ящик\n3) Третий ящик", "ожидался пункт 2"},
		{"after front matter", "---\nname: a\n---\n" + header + "Шаги 1 и 3 по саду", "нет шага 3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := tc.text
			if !strings.HasPrefix(text, "---\n") {
				text = header + text
			}
			path := filepath.Join(t.TempDir(), "SKILL.md")
			findings := withoutOrphanSkillFindings(findTextRefs(textCorpus{path: []byte(text)}, filepath.Join(t.TempDir(), "decisions.md")))
			for _, finding := range findings {
				if finding.kind == 4 && strings.Contains(finding.missing, tc.want) {
					return
				}
			}
			t.Fatalf("findings %+v: want kind 4 containing %q", findings, tc.want)
		})
	}
}
