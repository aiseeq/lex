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

	t.Run("shared refresh", func(t *testing.T) {
		root, codex, a, b := sharedEditFixture(t)
		data, err := os.ReadFile(a)
		if err != nil {
			t.Fatal(err)
		}
		data = bytes.Replace(data, []byte("shared para"), []byte("changed shared para"), 1)
		data = bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n"))
		mustWrite(t, a, data)

		entries, err := StatusWithHome(codex, root)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, entry := range entries {
			if entry.Output != a {
				continue
			}
			found = true
			if !strings.Contains(entry.Command, "--shared") || strings.Contains(entry.Command, "import --refresh") {
				t.Fatalf("status command = %q; want shared-safe advice", entry.Command)
			}
		}
		if !found {
			t.Fatalf("status has no entry for %s", a)
		}

		_, checkErr := CheckWithWarnings(codex, CheckOptions{})
		if checkErr == nil || !strings.Contains(checkErr.Error(), "--shared") || !strings.Contains(checkErr.Error(), b) || strings.Contains(checkErr.Error(), "import --refresh") {
			t.Fatalf("check advice = %v; want shared-safe advice naming %s", checkErr, b)
		}

		before := treeBytes(t, codex)
		err = ImportWithOptions(codex, a, ImportOptions{Refresh: true, Home: root})
		if err == nil || !strings.Contains(err.Error(), "CRLF") || !strings.Contains(err.Error(), "LF") || !strings.Contains(err.Error(), "lex import --refresh") {
			t.Fatalf("CRLF refresh error = %v; want whole-file CRLF refusal", err)
		}
		if !bytes.Equal(before, treeBytes(t, codex)) {
			t.Fatal("refused CRLF refresh changed codex")
		}
	})
}

func TestRefreshRefusesWholeFileCRLF(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "without-shared-fragment"
		if shared {
			name = "with-unchanged-shared-fragment"
		}
		t.Run(name, func(t *testing.T) {
			var root, codex, out string
			var other string
			if shared {
				root, codex, out, other = sharedEditFixture(t)
			} else {
				root = t.TempDir()
				codex = filepath.Join(root, "codex")
				out = filepath.Join(root, "docs", "a.md")
				if err := os.MkdirAll(filepath.Dir(out), directoryMode); err != nil {
					t.Fatal(err)
				}
				mustWrite(t, out, []byte("# A\n\nfirst para\n\nsecond para\n"))
				if err := ImportWithOptions(codex, out, ImportOptions{Home: root}); err != nil {
					t.Fatal(err)
				}
			}
			data, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			crlf := bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n"))
			mustWrite(t, out, crlf)
			beforeCodex := treeBytes(t, codex)

			err = ImportWithOptions(codex, out, ImportOptions{Refresh: true, Home: root})
			if err == nil || !strings.Contains(err.Error(), "CRLF") || !strings.Contains(err.Error(), "LF") || !strings.Contains(err.Error(), "lex import --refresh") {
				t.Fatalf("refresh error = %v; want whole-file CRLF refusal with refresh advice", err)
			}
			if !bytes.Equal(beforeCodex, treeBytes(t, codex)) {
				t.Fatal("refused refresh changed codex")
			}
			got, err := os.ReadFile(out)
			if err != nil || !bytes.Equal(got, crlf) {
				t.Fatalf("refused refresh changed output: got %q, err %v", got, err)
			}
			if shared {
				gotOther, err := os.ReadFile(other)
				if err != nil || !bytes.Equal(gotOther, []byte("# B\n\nshared para\n")) {
					t.Fatalf("refused refresh changed shared output: got %q, err %v", gotOther, err)
				}
			}

			entries, err := StatusWithHome(codex, root)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Output != out {
					continue
				}
				if strings.Contains(entry.Command, "import --refresh") || entry.Refusal == "" && !strings.Contains(entry.Command, "lex absorb") {
					t.Fatalf("status advice = %#v; want refusal or a command that refuses", entry)
				}
				return
			}
			t.Fatalf("status has no entry for %s", out)
		})
	}

	t.Run("CRLF source remains refreshable", func(t *testing.T) {
		root := t.TempDir()
		codex := filepath.Join(root, "codex")
		out := filepath.Join(root, "docs", "a.md")
		if err := os.MkdirAll(filepath.Dir(out), directoryMode); err != nil {
			t.Fatal(err)
		}
		original := []byte("# A\r\n\r\nfirst para\r\n")
		mustWrite(t, out, original)
		if err := ImportWithOptions(codex, out, ImportOptions{Home: root}); err != nil {
			t.Fatal(err)
		}
		updated := bytes.Replace(original, []byte("first para"), []byte("updated para"), 1)
		mustWrite(t, out, updated)
		if err := ImportWithOptions(codex, out, ImportOptions{Refresh: true, Home: root}); err != nil {
			t.Fatalf("refresh of CRLF source: %v", err)
		}
	})
}
