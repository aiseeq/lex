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
