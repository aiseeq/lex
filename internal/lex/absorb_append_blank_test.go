package lex

import (
	"os"
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
			if err := Build(codex, false); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.trailingNewline {
				data = []byte(strings.TrimSuffix(string(data), "\n"))
			}
			want := append(append([]byte(nil), data...), []byte("\n- four\n")...)
			mustWrite(t, out, want)
			if err := Absorb(codex, out, AbsorbOptions{DraftDir: codex}); err != nil {
				t.Fatal(err)
			}
			updated := mustReadManifest(t, manifestPath)
			for _, id := range updated.parts {
				body := readFragmentBody(t, codex, id)
				if len(body) > 0 && len(strings.TrimSpace(string(body))) == 0 {
					t.Errorf("fragment %q contains only blank lines: %q", id, body)
				}
			}
			assertAbsorbedOutputEquals(t, codex, out, want)
		})
	}
}
