package lex

import (
	"os"
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
			if err := Build(codex, false); err != nil {
				t.Fatal(err)
			}
			built, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			want := tc.edit(built)
			mustWrite(t, out, want)
			if err := Absorb(codex, out, AbsorbOptions{DraftDir: codex}); err != nil {
				t.Fatal(err)
			}
			updated := mustReadManifest(t, manifestPath)
			for _, id := range updated.parts {
				body := readFragmentBody(t, codex, id)
				if len(body) > 0 && len(strings.Fields(string(body))) == 0 {
					t.Errorf("fragment %q contains only blank lines: %q", id, body)
				}
			}
			assertAbsorbedOutputEquals(t, codex, out, want)
		})
	}
}
