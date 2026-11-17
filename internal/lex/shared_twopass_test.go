package lex

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sharedDropFixture(t *testing.T, paragraphs []string) (root, codex string, outputs map[string]string, sharedIDs []string) {
	return sharedDropFixtureWithShared(t, paragraphs, paragraphs)
}

func sharedDropFixtureWithShared(t *testing.T, paragraphs, sharedParagraphs []string) (root, codex string, outputs map[string]string, sharedIDs []string) {
	t.Helper()
	root = t.TempDir()
	codex = filepath.Join(root, "codex")
	outputs = map[string]string{}
	if err := os.MkdirAll(filepath.Join(root, "docs"), directoryMode); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		path := filepath.Join(root, "docs", name)
		outputs[name] = path
		var content strings.Builder
		fmt.Fprintf(&content, "# %s\n\n", strings.TrimSuffix(strings.ToUpper(name), ".MD"))
		for i, paragraph := range paragraphs {
			if i > 0 {
				content.WriteByte('\n')
			}
			content.WriteString(paragraph)
			content.WriteByte('\n')
		}
		mustWrite(t, path, []byte(content.String()))
		if err := ImportWithOptions(codex, path, ImportOptions{Home: root}); err != nil {
			t.Fatal(err)
		}
	}

	manifestPath := func(name string) string {
		return filepath.Join(codex, "manifest", codexKey("docs/"+name)+".yaml")
	}
	ma := mustReadManifest(t, manifestPath("a.md"))
	sharedSet := make(map[string]bool, len(sharedParagraphs))
	for _, paragraph := range sharedParagraphs {
		sharedSet[paragraph] = true
	}
	for _, paragraph := range paragraphs {
		id := sharedPartID(t, codex, ma, paragraph)
		sharedIDs = append(sharedIDs, id)
	}
	for _, name := range []string{"b.md", "c.md"} {
		path := manifestPath(name)
		m := mustReadManifest(t, path)
		for i, id := range m.parts {
			raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
			if err != nil {
				t.Fatal(err)
			}
			body, err := fragmentBody(raw)
			if err != nil {
				t.Fatal(err)
			}
			for n, paragraph := range paragraphs {
				if !sharedSet[paragraph] || !bytes.Contains(body, []byte(paragraph)) {
					continue
				}
				delete(m.fragmentHashes, id)
				m.parts[i] = sharedIDs[n]
				m.fragmentHashes[sharedIDs[n]] = ma.fragmentHashes[sharedIDs[n]]
				if err := os.Remove(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")); err != nil {
					t.Fatal(err)
				}
				break
			}
		}
		if err := writeManifest(path, m, m.links); err != nil {
			t.Fatal(err)
		}
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	return root, codex, outputs, sharedIDs
}

func sharedPartID(t *testing.T, codex string, m manifest, paragraph string) string {
	t.Helper()
	for _, id := range m.parts {
		raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
		if err != nil {
			t.Fatal(err)
		}
		body, err := fragmentBody(raw)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, []byte(paragraph)) {
			return id
		}
	}
	t.Fatalf("shared fragment for %q not found", paragraph)
	return ""
}

func assertTwoPassAdvice(t *testing.T, err error, ids []string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected refusal")
	}
	message := err.Error()
	for _, id := range ids {
		if !strings.Contains(message, "`"+id+"`") {
			t.Errorf("refusal %q does not name shared fragment %q", message, id)
		}
	}
	for _, want := range []string{"first delete only", "still used by", "b.md", "c.md", "run `lex absorb`", "then make the other edit and run `lex absorb` again"} {
		if !strings.Contains(message, want) {
			t.Errorf("refusal %q does not contain %q", message, want)
		}
	}
}

func TestSharedDropAdvisesTwoPasses(t *testing.T) {
	for _, tc := range []struct {
		name       string
		paragraphs []string
		refresh    bool
	}{
		{name: "absorb"},
		{name: "refresh own boundary branch", refresh: true},
		{name: "absorb multiple", paragraphs: []string{"shared para", "second shared"}},
		{name: "refresh multiple", paragraphs: []string{"shared para", "second shared"}, refresh: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paragraphs := tc.paragraphs
			if len(paragraphs) == 0 {
				paragraphs = []string{"shared para"}
			}
			root, codex, outputs, ids := sharedDropFixture(t, paragraphs)
			mustWrite(t, outputs["a.md"], []byte("x\n"))
			var refusal error
			if tc.refresh {
				refusal = ImportWithOptions(codex, outputs["a.md"], ImportOptions{Refresh: true, Home: root})
			} else {
				refusal = Absorb(codex, outputs["a.md"], AbsorbOptions{DraftDir: codex})
			}
			assertTwoPassAdvice(t, refusal, ids)
			if tc.refresh && !strings.Contains(refusal.Error(), "refresh would remove a fragment still used by") {
				t.Fatalf("refresh refusal = %v; want refresh's shared-removal advice", refusal)
			}
			if tc.refresh && !strings.Contains(refusal.Error(), "run `lex absorb --codex ") {
				t.Fatalf("refresh refusal lost its existing absorb command: %v", refusal)
			}

			// The first pass removes every shared paragraph; the second applies the other edit.
			mustWrite(t, outputs["a.md"], []byte("# A\n"))
			if err := Absorb(codex, outputs["a.md"], AbsorbOptions{}); err != nil {
				t.Fatalf("first pass absorb: %v", err)
			}
			mustWrite(t, outputs["a.md"], []byte("x\n"))
			if err := Absorb(codex, outputs["a.md"], AbsorbOptions{}); err != nil {
				t.Fatalf("second pass absorb: %v", err)
			}
			if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
				t.Fatalf("check after advised path: %v", err)
			}
		})
	}
}

func TestSharedDropAdviceRequiresWholeParagraphRemoval(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		name := "absorb"
		if refresh {
			name = "refresh"
		}
		t.Run(name, func(t *testing.T) {
			root, codex, outputs, _ := sharedDropFixture(t, []string{"shared para"})
			edited := []byte("# A\n\nSHARED para\n")
			mustWrite(t, outputs["a.md"], edited)
			var err error
			if refresh {
				err = ImportWithOptions(codex, outputs["a.md"], ImportOptions{Refresh: true, Home: root})
			} else {
				err = Absorb(codex, outputs["a.md"], AbsorbOptions{DraftDir: codex})
			}
			if err == nil || strings.Contains(err.Error(), "first delete only") {
				t.Fatalf("refusal should retain its prior text without two-pass advice: %v", err)
			}
			want := "shared fragment affects"
			if refresh {
				want = "refresh would edit a shared fragment"
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("refusal = %v; want prior refusal containing %q", err, want)
			}
		})
	}
}

func TestSharedDropAdviceSkipsParagraphPresentInBoundaryConflict(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	outputs := make(map[string]string)
	if err := os.MkdirAll(filepath.Join(root, "docs"), directoryMode); err != nil {
		t.Fatal(err)
	}
	original := []byte("# One\na\n\n# Two\nb\n")
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		outputs[name] = filepath.Join(root, "docs", name)
		mustWrite(t, outputs[name], original)
		if err := ImportWithOptions(codex, outputs[name], ImportOptions{Home: root}); err != nil {
			t.Fatal(err)
		}
	}
	manifestPath := func(name string) string {
		return filepath.Join(codex, "manifest", codexKey("docs/"+name)+".yaml")
	}
	ma := mustReadManifest(t, manifestPath("a.md"))
	sharedID := sharedPartID(t, codex, ma, "b")
	for _, name := range []string{"b.md", "c.md"} {
		path := manifestPath(name)
		m := mustReadManifest(t, path)
		for i, id := range m.parts {
			raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
			if err != nil {
				t.Fatal(err)
			}
			body, err := fragmentBody(raw)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(body, []byte("b")) {
				continue
			}
			delete(m.fragmentHashes, id)
			m.parts[i] = sharedID
			m.fragmentHashes[sharedID] = ma.fragmentHashes[sharedID]
			if err := os.Remove(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")); err != nil {
				t.Fatal(err)
			}
			break
		}
		if err := writeManifest(path, m, m.links); err != nil {
			t.Fatal(err)
		}
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	edited := []byte("# One\na\nX\nTWO\nb\n")
	mustWrite(t, outputs["a.md"], edited)
	err := Absorb(codex, outputs["a.md"], AbsorbOptions{DraftDir: codex})
	if err == nil || !strings.Contains(err.Error(), "boundary conflict") || strings.Contains(err.Error(), "first delete only") || strings.Contains(err.Error(), sharedID) {
		t.Fatalf("boundary refusal = %v; want the old text while the shared paragraph remains", err)
	}
}

func TestSharedDropStatusAndCheckKeepNeighborEntries(t *testing.T) {
	root, codex, outputs, _ := sharedDropFixture(t, []string{"shared para"})
	d := filepath.Join(root, "docs", "d.md")
	mustWrite(t, d, []byte("# D\n\noriginal d\n"))
	if err := ImportWithOptions(codex, d, ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, outputs["a.md"], []byte("x\n"))
	mustWrite(t, d, []byte("# D\n\nchanged d\n"))
	entries, err := StatusWithHome(codex, root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	found := map[string]bool{}
	for _, entry := range entries {
		found[entry.Output] = true
	}
	if !found[outputs["a.md"]] || !found[d] {
		t.Fatalf("status outputs = %#v, want entries for a.md and d.md", entries)
	}
	_, err = CheckWithWarnings(codex, CheckOptions{})
	if err == nil || !strings.Contains(err.Error(), outputs["a.md"]) || !strings.Contains(err.Error(), d) || strings.Contains(err.Error(), "a.md: a.md:") || strings.Contains(err.Error(), "draft directory must be provided") {
		t.Fatalf("check errors = %v; want separate a.md and d.md problems", err)
	}
}

func TestSharedDropForceRefusalKeepsBothChoices(t *testing.T) {
	root, codex, outputs, ids := sharedDropFixture(t, []string{"shared para"})
	fragmentPath := filepath.Join(codex, "frag", filepath.FromSlash(ids[0])+".md")
	raw, err := os.ReadFile(fragmentPath)
	if err != nil {
		t.Fatal(err)
	}
	body, err := fragmentBody(raw)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, fragmentPath, replaceFragmentBody(raw, body, bytes.Replace(body, []byte("shared para"), []byte("fragment changed"), 1)))
	mustWrite(t, outputs["a.md"], []byte("x\n"))
	err = Absorb(codex, outputs["a.md"], AbsorbOptions{})
	if err == nil || !strings.Contains(err.Error(), "lex absorb --force --codex") || !strings.Contains(err.Error(), "lex build --force --codex") {
		t.Fatalf("force refusal = %v; want both recovery choices", err)
	}
	_, checkErr := CheckWithWarnings(codex, CheckOptions{})
	if checkErr == nil || !strings.Contains(checkErr.Error(), "lex absorb --force --codex") || !strings.Contains(checkErr.Error(), "lex build --force --codex") {
		t.Fatalf("check force refusal = %v; want both recovery choices", checkErr)
	}
	entries, err := StatusWithHome(codex, root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, entry := range entries {
		if entry.Output != outputs["a.md"] {
			continue
		}
		if !strings.Contains(entry.Command, "lex absorb --force --codex") || !strings.Contains(entry.KeepFragments, "lex build --force --codex") {
			t.Fatalf("status lost force choices: %#v", entry)
		}
		return
	}
	t.Fatalf("status has no entry for %s", outputs["a.md"])
}
