package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAbsorbRefusesWholeFileCRLF(t *testing.T) {
	for _, withHeader := range []bool{false, true} {
		for _, edited := range []bool{false, true} {
			name := "without-header"
			if withHeader {
				name = "with-header"
			}
			if edited {
				name += "/edited"
			} else {
				name += "/unmodified"
			}
			t.Run(name, func(t *testing.T) {
				codex, out := t.TempDir(), filepath.Join(t.TempDir(), "guide.md")
				if err := importData(codex, out, "guide.md", []byte("# Guide\n\n- first\n\n- second\n"), false); err != nil {
					t.Fatal(err)
				}
				if withHeader {
					if err := setManifestHeader(codex, "guide.md", true); err != nil {
						t.Fatal(err)
					}
				}
				if err := Build(codex, false); err != nil {
					t.Fatal(err)
				}
				original, err := os.ReadFile(out)
				if err != nil {
					t.Fatal(err)
				}
				if edited {
					original = bytes.Replace(original, []byte("- first"), []byte("- changed first"), 1)
				}
				crlf := bytes.ReplaceAll(original, []byte("\n"), []byte("\r\n"))
				mustWrite(t, out, crlf)
				beforeCodex := treeBytes(t, codex)

				err = Absorb(codex, out, AbsorbOptions{DraftDir: codex})
				if err == nil || !strings.Contains(err.Error(), "CRLF") ||
					!strings.Contains(err.Error(), "LF") {
					t.Fatalf("absorb error = %v; want CRLF refusal advising return to LF", err)
				}
				if !bytes.Equal(beforeCodex, treeBytes(t, codex)) {
					t.Fatal("refused absorb changed codex")
				}
				got, err := os.ReadFile(out)
				if err != nil || !bytes.Equal(got, crlf) {
					t.Fatalf("refused absorb changed output: got %q, err %v", got, err)
				}
				drafts, err := filepath.Glob(filepath.Join(codex, "drafts", "*.md"))
				if err != nil {
					t.Fatal(err)
				}
				if len(drafts) != 0 {
					t.Fatalf("refused absorb wrote drafts: %v", drafts)
				}
				if edited {
					mustWrite(t, out, bytes.ReplaceAll(crlf, []byte("\r\n"), []byte("\n")))
					if err := Absorb(codex, out, AbsorbOptions{DraftDir: codex}); err != nil {
						t.Fatalf("absorb after restoring LF: %v", err)
					}
					m := mustReadManifest(t, filepath.Join(codex, "manifest", codexKey("guide.md")+".yaml"))
					var bodies []byte
					for _, id := range m.parts {
						bodies = append(bodies, readFragmentBody(t, codex, id)...)
					}
					if !bytes.Contains(bodies, []byte("- changed first")) {
						t.Fatalf("restored-LF edit was not absorbed into fragments: %q", bodies)
					}
				}
			})
		}
	}
}
