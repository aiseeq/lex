package lex

import (
	"strings"
	"testing"
)

func TestAbsorbTrailingBlankLinesLeaveNoBlankFragment(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func([]byte) []byte
	}{
		{name: "one trailing blank line", edit: func(data []byte) []byte { return append(data, '\n') }},
		{name: "two trailing blank lines", edit: func(data []byte) []byte { return append(data, '\n', '\n') }},
		{name: "extra blank before middle insertion", edit: func(data []byte) []byte {
			return []byte(strings.Replace(string(data), "- one\n", "- one\n\n\n- newA\n", 1))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codex, out, manifestPath, _ := purposeFixture(t, "# T\n\n- one\n\n- two\n\n- three\n")
			want := prepareBlankAbsorbEdit(t, codex, out, tc.edit)
			finishBlankAbsorb(t, codex, out, manifestPath, want)
		})
	}
}
