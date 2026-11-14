package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func prepareBlankAbsorbEdit(t *testing.T, codex, out string, edit func([]byte) []byte) []byte {
	t.Helper()
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	built, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	want := edit(built)
	mustWrite(t, out, want)
	return want
}

func finishBlankAbsorb(t *testing.T, codex, out, manifestPath string, want []byte) {
	t.Helper()
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
}

func TestAbsorbInsertedBlankLinesLeaveNoBlankFragment(t *testing.T) {
	for _, tc := range []struct {
		name    string
		input   string
		edit    func([]byte) []byte
		purpose bool
	}{
		{name: "one at start", input: "# T\n\n- one\n\n- two\n\n- three\n", edit: func(data []byte) []byte { return append([]byte("\n"), data...) }},
		{name: "two at start", input: "# T\n\n- one\n\n- two\n\n- three\n", edit: func(data []byte) []byte { return append([]byte("\n\n"), data...) }},
		{name: "between paragraphs", input: "# T\n\n- one\n\n- two\n\n- three\n", edit: func(data []byte) []byte { return bytes.Replace(data, []byte("- one\n\n"), []byte("- one\n\n\n"), 1) }},
		{name: "between paragraphs CRLF", input: "# T\n\n- one\n\n- two\n\n- three\n", edit: func(data []byte) []byte { return bytes.Replace(data, []byte("- one\n\n"), []byte("- one\n\r\n\n"), 1) }},
		{name: "between paragraphs with purpose", input: "# T\n\n- one\n\n- two\n\n- three\n", edit: func(data []byte) []byte { return bytes.Replace(data, []byte("- two\n\n"), []byte("- two\n\n\n"), 1) }, purpose: true},
		{name: "ws CR around", input: "# T\n\n- one\n\n- two\n\n- three\n", edit: func(data []byte) []byte {
			return bytes.Replace(data, []byte("- one\n\n"), []byte("- one\n\r\n\n\r\n"), 1)
		}},
		{name: "ws spaces around", input: "# T\n\n- one\n\n- two\n\n- three\n", edit: func(data []byte) []byte {
			return bytes.Replace(data, []byte("- one\n\n"), []byte("- one\n \n\n \n"), 1)
		}},
		{name: "ws tabs around", input: "# T\n\n- one\n\n- two\n\n- three\n", edit: func(data []byte) []byte {
			return bytes.Replace(data, []byte("- one\n\n"), []byte("- one\n\t\n\n\t\n"), 1)
		}},
		{name: "ws CR around with purpose", input: "# T\n\n- one\n\n- two\n\n- three\n", edit: func(data []byte) []byte {
			return bytes.Replace(data, []byte("- two\n\n"), []byte("- two\n\r\n\n\r\n"), 1)
		}, purpose: true},
		{name: "ws CR before new paragraph", input: "# T\n\n- one\n\n- two\n\n- three\n", edit: func(data []byte) []byte {
			return bytes.Replace(data, []byte("- one\n\n"), []byte("- one\n\r\n\n- new\n\n"), 1)
		}},
		{name: "ws space before new paragraph", input: "# T\n\n- one\n\n- two\n\n- three\n", edit: func(data []byte) []byte {
			return bytes.Replace(data, []byte("- one\n\n"), []byte("- one\n \n\n- new\n\n"), 1)
		}},
		{name: "ws CR before new and edit", input: "# T\n\n- one\n\n- two\n\n- three\n", edit: func(data []byte) []byte {
			data = bytes.Replace(data, []byte("- one\n\n"), []byte("- one\n\r\n\n- new\n\n"), 1)
			return bytes.Replace(data, []byte("- three"), []byte("- THREE"), 1)
		}},
		{name: "between and edit", input: "# T\n\n- one\n\n- two\n\n- three\n", edit: func(data []byte) []byte {
			data = bytes.Replace(data, []byte("- one\n\n"), []byte("- one\n\n\n"), 1)
			return bytes.Replace(data, []byte("- three\n"), []byte("- THREE\n"), 1)
		}},
		{name: "start and append", input: "# T\n\n- one\n\n- two\n\n- three\n", edit: func(data []byte) []byte {
			return append(append([]byte("\n"), data...), []byte("\n- four\n")...)
		}},
		{name: "between and insert", input: "# T\n\n- one\n\n- two\n\n- three\n", edit: func(data []byte) []byte {
			data = bytes.Replace(data, []byte("- one\n\n"), []byte("- one\n\n\n"), 1)
			return bytes.Replace(data, []byte("- two\n"), []byte("- two\n\n- newB\n"), 1)
		}},
		{name: "start and edit", input: "# T\n\n- one\n\n- two\n\n- three\n", edit: func(data []byte) []byte {
			data = append([]byte("\n"), data...)
			return bytes.Replace(data, []byte("- two\n"), []byte("- TWO\n"), 1)
		}},
		{name: "insert paragraph adjacent to blank", input: "# Guide\n\nIntro line.\n\n## Tasks\n- rule one text\n- rule two text\n- rule three text\n- rule four text\n- rule five text\n\n## Work\n- rule six text\n- rule seven text\n", edit: func(data []byte) []byte {
			data = bytes.Replace(data, []byte("- rule four text\n"), nil, 1)
			return bytes.Replace(data, []byte("- rule five text\n"), []byte("- rule five text\nnew para 4\n\n"), 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codex, out, manifestPath, _ := purposeFixture(t, tc.input)
			if tc.purpose {
				purposeSet(t, codex, true)
				m := mustReadManifest(t, manifestPath)
				purposeFragment(t, codex, manifestPath, m, len(m.parts)-1, "why")
			}
			want := prepareBlankAbsorbEdit(t, codex, out, tc.edit)
			finishBlankAbsorb(t, codex, out, manifestPath, want)
		})
	}
}

func TestAbsorbBlankChainKeepsNewParagraph(t *testing.T) {
	source := "# T\n\n- one\n\n\n- two\n\n- three\n"
	for _, tc := range []struct {
		name string
		edit func([]byte) []byte
	}{
		{name: "blank text blank", edit: func(data []byte) []byte {
			data = bytes.Replace(data, []byte("- one\n\n\n"), []byte("- one\n \n\n- new\n\n \n\n"), 1)
			return bytes.Replace(data, []byte("- three"), []byte("- THREE"), 1)
		}},
		{name: "blank text", edit: func(data []byte) []byte {
			data = bytes.Replace(data, []byte("- one\n\n\n"), []byte("- one\n \n\n- new\n\n\n"), 1)
			return bytes.Replace(data, []byte("- three"), []byte("- THREE"), 1)
		}},
		{name: "five parts", edit: func(data []byte) []byte {
			data = bytes.Replace(data, []byte("- one\n\n\n"), []byte("- one\n \n\n- A\n\n \n\n- B\n\n \n\n"), 1)
			return bytes.Replace(data, []byte("- three"), []byte("- THREE"), 1)
		}},
		{name: "plain paragraph", edit: func(data []byte) []byte {
			data = bytes.Replace(data, []byte("- one\n\n\n"), []byte("- one\n \n\nNew para\n\n \n\n"), 1)
			return bytes.Replace(data, []byte("- three"), []byte("- THREE"), 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codex, out, _, _ := purposeFixture(t, source)
			if err := Build(codex, false); err != nil {
				t.Fatal(err)
			}
			built, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			want := tc.edit(built)
			mustWrite(t, out, want)
			beforeCodex := treeBytes(t, codex)
			changed, err := AbsorbWithPaths(codex, out, AbsorbOptions{})
			if err != nil {
				if len(changed) != 0 || !bytes.Equal(beforeCodex, treeBytes(t, codex)) {
					t.Fatalf("refused absorb changed the codex: %v", changed)
				}
				got, readErr := os.ReadFile(out)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if !bytes.Equal(got, want) {
					t.Fatal("refused absorb changed the output file")
				}
				return
			}
			assertAbsorbedOutputEquals(t, codex, out, want)
			if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
				t.Fatalf("lex check: %v", err)
			}
			for _, id := range mustReadManifest(t, filepath.Join(codex, "manifest", "out.md.yaml")).parts {
				body := string(readFragmentBody(t, codex, id))
				if len(strings.TrimSpace(body)) == 0 {
					t.Errorf("fragment %q contains only blank lines", id)
				}
				newParagraph := strings.Contains(body, "- new") || strings.Contains(body, "- A") || strings.Contains(body, "- B") || strings.Contains(body, "New para")
				oldParagraph := strings.Contains(body, "- one") || strings.Contains(body, "- two") || strings.Contains(body, "- three")
				if newParagraph && oldParagraph {
					t.Errorf("fragment %q mixes a new paragraph with old text: %q", id, body)
				}
			}
		})
	}
}

func TestAbsorbTrailingBlankLinesSharedLastFragment(t *testing.T) {
	codex := t.TempDir()
	outA, outB := filepath.Join(t.TempDir(), "a.md"), filepath.Join(t.TempDir(), "b.md")
	if err := importData(codex, outA, "a.md", []byte("alpha\n\nshared\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := importData(codex, outB, "b.md", []byte("shared\n"), false); err != nil {
		t.Fatal(err)
	}
	ma, err := readManifest(filepath.Join(codex, "manifest", codexKey("a.md")+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	mb, err := readManifest(filepath.Join(codex, "manifest", codexKey("b.md")+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	shared := ma.parts[len(ma.parts)-1]
	orphan := mb.parts[0]
	raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(shared)+".md"))
	if err != nil {
		t.Fatal(err)
	}
	mb.parts, mb.fragmentHashes = []string{shared}, map[string]string{shared: sha(raw)}
	if err := writeManifest(filepath.Join(codex, "manifest", codexKey("b.md")+".yaml"), mb, mb.links); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(codex, "frag", filepath.FromSlash(orphan)+".md")); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	built, err := os.ReadFile(outA)
	if err != nil {
		t.Fatal(err)
	}
	want := append(built, 10)
	mustWrite(t, outA, want)
	if err := Absorb(codex, outA, AbsorbOptions{}); err == nil || !strings.Contains(err.Error(), "--shared") {
		t.Fatalf("expected --shared refusal, got %v", err)
	}
	if err := Absorb(codex, outA, AbsorbOptions{Shared: true}); err != nil {
		t.Fatal(err)
	}
	assertAbsorbedOutputEquals(t, codex, outA, want)
}

func TestAbsorbReplacedLeadingBlankEditsItsFragment(t *testing.T) {
	for _, blank := range []string{" \n", "\r\n"} {
		t.Run(strconv.Quote(blank), func(t *testing.T) {
			codex, out, manifestPath, _ := purposeFixture(t, "# T\n\n- one\n\n- two\n\n- three\n")
			want := prepareBlankAbsorbEdit(t, codex, out, func(data []byte) []byte {
				return bytes.Replace(data, []byte("- one\n\n"), []byte("- one\n\n\n- new\n\n"), 1)
			})
			finishBlankAbsorb(t, codex, out, manifestPath, want)
			want = prepareBlankAbsorbEdit(t, codex, out, func(data []byte) []byte {
				return bytes.Replace(data, []byte("- one\n\n\n- new"), []byte("- one\n\n"+blank+"- new"), 1)
			})
			finishBlankAbsorb(t, codex, out, manifestPath, want)
		})
	}
}
