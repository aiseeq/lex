package lex

import (
	"strings"
	"testing"
)

func TestAbsorbAppendLeavesNoBlankFragment(t *testing.T) {
	base := "# T\n\n- one\n\n- two\n\n- three"
	for _, tc := range []struct {
		name            string
		trailingNewline bool
	}{
		{name: "with trailing newline", trailingNewline: true},
		{name: "without trailing newline", trailingNewline: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := base
			if tc.trailingNewline {
				input += "\n"
			}
			codex, out, manifestPath, _ := purposeFixture(t, input)
			want := prepareBlankAbsorbEdit(t, codex, out, func(data []byte) []byte {
				if !tc.trailingNewline {
					data = []byte(strings.TrimSuffix(string(data), "\n"))
				}
				return append(append([]byte(nil), data...), []byte("\n- four\n")...)
			})
			finishBlankAbsorb(t, codex, out, manifestPath, want)
		})
	}
}
