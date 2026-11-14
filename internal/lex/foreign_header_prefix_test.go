package lex

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestForeignHeaderBehindInvisiblePrefixRefused(t *testing.T) {
	prefixes := []struct {
		name string
		data []byte
	}{
		{name: "BOM", data: []byte("\xef\xbb\xbf")},
		{name: "ZWSP", data: []byte("\xe2\x80\x8b")},
		{name: "BOM and space", data: []byte("\xef\xbb\xbf ")},
		{name: "BOM before front matter", data: []byte("\xef\xbb\xbf---\nname: x\n---\n")},
	}
	for _, prefix := range prefixes {
		t.Run(prefix.name, func(t *testing.T) {
			t.Run("import", func(t *testing.T) {
				dir := t.TempDir()
				codex := filepath.Join(dir, "codex-b")
				out := filepath.Join(dir, "source.md")
				foreign := append(append([]byte(nil), prefix.data...), generatedMarkerForCodex("source", filepath.Join(dir, "codex-a"))...)
				foreign = append(foreign, []byte("# Source\n")...)
				mustWrite(t, out, foreign)
				err := ImportWithOptions(codex, out, ImportOptions{Home: dir})
				if err == nil || !strings.Contains(err.Error(), "output belongs to codex") || !strings.Contains(err.Error(), filepath.Join(dir, "codex-a")) {
					t.Fatalf("import = %v, want foreign codex refusal", err)
				}
			})

			for _, operation := range []string{"absorb", "refresh"} {
				t.Run(operation, func(t *testing.T) {
					dir := t.TempDir()
					out := filepath.Join(dir, "source.md")
					codex := filepath.Join(dir, "codex-b")
					body := []byte("# Source\n\n- one\n")
					mustWrite(t, out, body)
					if err := ImportWithOptions(codex, out, ImportOptions{Home: dir}); err != nil {
						t.Fatal(err)
					}
					foreign := append(generatedMarkerForCodex("source", filepath.Join(dir, "codex-a")), body...)
					mustWrite(t, out, append(append([]byte(nil), prefix.data...), foreign...))
					var err error
					if operation == "absorb" {
						err = Absorb(codex, out, AbsorbOptions{})
					} else {
						err = ImportWithOptions(codex, out, ImportOptions{Refresh: true, Home: dir})
					}
					if err == nil || !strings.Contains(err.Error(), "output belongs to codex") {
						t.Fatalf("%s = %v, want foreign codex refusal", operation, err)
					}
				})
			}

			t.Run("check and status match unprefixed advice", func(t *testing.T) {
				dir := t.TempDir()
				out := filepath.Join(dir, "source.md")
				codex := filepath.Join(dir, "codex-b")
				body := []byte("# Source\n\n- one\n")
				mustWrite(t, out, body)
				if err := ImportWithOptions(codex, out, ImportOptions{Home: dir}); err != nil {
					t.Fatal(err)
				}
				foreign := append(generatedMarkerForCodex("source", filepath.Join(dir, "codex-a")), body...)
				mustWrite(t, out, foreign)
				_, baseCheckErr := CheckWithWarnings(codex, CheckOptions{})
				if baseCheckErr == nil {
					t.Fatal("check succeeded, want foreign codex advice")
				}
				baseStatus, err := Status(codex)
				if err != nil || len(baseStatus) != 1 {
					t.Fatalf("status without prefix = %#v, %v", baseStatus, err)
				}

				mustWrite(t, out, append(append([]byte(nil), prefix.data...), foreign...))
				_, prefixedCheckErr := CheckWithWarnings(codex, CheckOptions{})
				if prefixedCheckErr == nil || prefixedCheckErr.Error() != baseCheckErr.Error() {
					t.Fatalf("check with prefix = %v, without prefix = %v", prefixedCheckErr, baseCheckErr)
				}
				prefixedStatus, err := Status(codex)
				if err != nil || len(prefixedStatus) != 1 {
					t.Fatalf("status with prefix = %#v, %v", prefixedStatus, err)
				}
				if baseStatus[0].ForeignCodex == "" || prefixedStatus[0].ForeignCodex != baseStatus[0].ForeignCodex || prefixedStatus[0].Command != baseStatus[0].Command || prefixedStatus[0].Refusal != baseStatus[0].Refusal {
					t.Fatalf("status with prefix = %#v, without prefix = %#v", prefixedStatus[0], baseStatus[0])
				}
			})
		})
	}
}

// A worktree copy with a byte order mark before its front matter keeps the foreign header on the
// line after the front matter; the advice names that line, not the fence the mark stands on.
func TestForeignHeaderActionNamesLineBehindPrefixedFrontMatter(t *testing.T) {
	dir := t.TempDir()
	body := "# B\n\n- one\n"
	actual := []byte("\xef\xbb\xbf---\nname: x\n---\n" + string(generatedMarkerForCodex("source", filepath.Join(dir, "codex-a"))) + body)
	m := manifest{out: filepath.Join(dir, "b.md")}
	for _, header := range []bool{false, true} {
		m.header = header
		manifestPath := filepath.Join(dir, "codex-b", "manifest", "b.md.yaml")
		action := (&outputCopy{}).foreignHeaderAction(actual, []byte("---\nname: x\n---\n"+body), m, manifestPath)
		if !strings.Contains(action, "line 4, the foreign header") {
			t.Fatalf("header=%v: action = %q, want line 4", header, action)
		}
	}
}
