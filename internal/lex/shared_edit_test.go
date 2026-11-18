package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sharedEditFixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	a, b := filepath.Join(root, "docs", "a.md"), filepath.Join(root, "docs", "b.md")
	if err := os.MkdirAll(filepath.Dir(a), directoryMode); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, a, []byte("# A\n\nshared para\n"))
	mustWrite(t, b, []byte("# B\n\nshared para\n"))
	if err := ImportWithOptions(codex, a, ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, b, ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	maPath := filepath.Join(codex, "manifest", codexKey("docs/a.md")+".yaml")
	mbPath := filepath.Join(codex, "manifest", codexKey("docs/b.md")+".yaml")
	ma, err := readManifest(maPath)
	if err != nil {
		t.Fatal(err)
	}
	mb, err := readManifest(mbPath)
	if err != nil {
		t.Fatal(err)
	}
	shared := ""
	for _, id := range ma.parts {
		raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
		if err != nil {
			t.Fatal(err)
		}
		body, err := fragmentBody(raw)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "shared para") {
			shared = id
		}
	}
	if shared == "" {
		t.Fatal("shared paragraph fragment not found")
	}
	for i, id := range mb.parts {
		if strings.Contains(id, "shared-para") {
			delete(mb.fragmentHashes, id)
			mb.parts[i] = shared
			mb.fragmentHashes[shared] = ma.fragmentHashes[shared]
			if err := os.Remove(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if err := writeManifest(mbPath, mb, mb.links); err != nil {
		t.Fatal(err)
	}
	return root, codex, a, b
}

func TestCheckAdviceSharedEdit(t *testing.T) {
	_, codex, a, b := sharedEditFixture(t)
	data, _ := os.ReadFile(a)
	mustWrite(t, a, []byte(strings.Replace(string(data), "shared para", "SHARED para", 1)))
	_, err := CheckWithWarnings(codex, CheckOptions{})
	if err == nil || !strings.Contains(err.Error(), "--shared") || !strings.Contains(err.Error(), b) {
		t.Fatalf("check advice = %v", err)
	}
}

func TestFirstSharedEditPreservesManifestOrder(t *testing.T) {
	paths := []string{"manifest-0", "manifest-1", "manifest-2"}
	catalog := newManifestCatalog(paths)
	for i, output := range []string{"a-b.md", "a+b.md", "c.md"} {
		catalog.items[paths[i]] = manifest{out: output, parts: []string{"shared"}}
	}

	affected, err := firstSharedEdit([]string{"shared"}, []int{0}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(affected, ", "), "a-b.md, a+b.md, c.md"; got != want {
		t.Fatalf("shared edit order = %q, want %q", got, want)
	}
}

func TestSharedForceHintSortsOutputs(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	outputs := make(map[string]string)
	if err := os.MkdirAll(filepath.Join(root, "docs"), directoryMode); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"c", "a+b", "a-b"} {
		path := filepath.Join(root, "docs", name+".md")
		outputs[name] = path
		mustWrite(t, path, []byte("# "+name+"\n\nshared para\n"))
		if err := ImportWithOptions(codex, path, ImportOptions{Home: root}); err != nil {
			t.Fatal(err)
		}
	}

	manifestPath := func(name string) string {
		return filepath.Join(codex, "manifest", codexKey("docs/"+name+".md")+".yaml")
	}
	selected, err := readManifest(manifestPath("c"))
	if err != nil {
		t.Fatal(err)
	}
	sharedID := ""
	for _, id := range selected.parts {
		raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
		if err != nil {
			t.Fatal(err)
		}
		body, err := fragmentBody(raw)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, []byte("shared para")) {
			sharedID = id
			break
		}
	}
	if sharedID == "" {
		t.Fatal("selected output has no shared paragraph fragment")
	}
	for _, name := range []string{"a+b", "a-b"} {
		path := manifestPath(name)
		m, err := readManifest(path)
		if err != nil {
			t.Fatal(err)
		}
		for i, id := range m.parts {
			raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
			if err != nil {
				t.Fatal(err)
			}
			body, err := fragmentBody(raw)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(body, []byte("shared para")) {
				continue
			}
			delete(m.fragmentHashes, id)
			m.parts[i] = sharedID
			m.fragmentHashes[sharedID] = selected.fragmentHashes[sharedID]
			if id != sharedID {
				if err := os.Remove(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")); err != nil {
					t.Fatal(err)
				}
			}
			if err := writeManifest(path, m, m.links); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	catalog, err := listManifestCatalog(codex)
	if err != nil {
		t.Fatal(err)
	}
	selected, err = readManifest(manifestPath("c"))
	if err != nil {
		t.Fatal(err)
	}
	layout, err := readPlainPartLayout(codex, selected)
	if err != nil {
		t.Fatal(err)
	}
	actual := bytes.Replace(layout.rendered, []byte("shared para"), []byte("shared para EDIT"), 1)
	flag, hint, err := codexSharedForceHintDetailed(codex, selected, layout.rendered, actual, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if flag != " --shared" {
		t.Fatalf("shared flag = %q, want --shared", flag)
	}
	want := "; --shared lets this file replace the fragments shared with " + outputs["a+b"] + ", " + outputs["a-b"] + ":"
	if !strings.Contains(hint, want) {
		t.Fatalf("shared hint = %q, want sorted path order %q", hint, want)
	}
}

func TestFirstSharedOtherOutputsDeduplicatesSameOutput(t *testing.T) {
	paths := []string{"manifest-selected", "manifest-one", "manifest-two"}
	catalog := newManifestCatalog(paths)
	for i, output := range []string{"selected.md", "shared.md", "shared.md"} {
		catalog.items[paths[i]] = manifest{out: output, parts: []string{"shared"}}
	}

	others, err := firstSharedOtherOutputs([]string{"shared"}, []int{0}, "selected.md", catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(others) != 1 || others[0] != "shared.md" {
		t.Fatalf("other outputs = %#v, want one shared.md", others)
	}
}

func TestStatusAdviceSharedEdit(t *testing.T) {
	root, codex, a, b := sharedEditFixture(t)
	data, _ := os.ReadFile(a)
	mustWrite(t, a, []byte(strings.Replace(string(data), "shared para", "SHARED para", 1)))
	entries, err := StatusWithHome(codex, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Output == a {
			if !strings.Contains(entry.Command, "--shared") || !strings.Contains(entry.SharedHint, b) {
				t.Fatalf("status advice = %#v", entry)
			}
			return
		}
	}
	t.Fatalf("status has no entry for %s", a)
}

func TestRefreshRefusesSharedEdit(t *testing.T) {
	root, codex, a, b := sharedEditFixture(t)
	data, _ := os.ReadFile(a)
	mustWrite(t, a, []byte(strings.Replace(string(data), "shared para", "SHARED para", 1)))
	err := ImportWithOptions(codex, a, ImportOptions{Refresh: true, Home: root})
	if err == nil || !strings.Contains(err.Error(), b) || !strings.Contains(err.Error(), "lex absorb --shared") {
		t.Fatalf("refresh error = %v", err)
	}
}

func TestRefreshKeepsFragmentOtherFileBuilds(t *testing.T) {
	for _, tc := range []struct {
		name, content string
	}{
		{name: "deleted", content: "# A\n"},
		{name: "tail added", content: "# A\n\nshared para\ntail line\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, codex, a, b := sharedEditFixture(t)
			mustWrite(t, a, []byte(tc.content))
			before, err := snapshotCodexFiles(codex)
			if err != nil {
				t.Fatal(err)
			}
			err = ImportWithOptions(codex, a, ImportOptions{Refresh: true, Home: root})
			if err == nil || !strings.Contains(err.Error(), b) || !strings.Contains(err.Error(), "lex absorb --codex") {
				t.Fatalf("refresh error = %v", err)
			}
			after, err := snapshotCodexFiles(codex)
			if err != nil {
				t.Fatal(err)
			}
			if len(before) != len(after) {
				t.Fatalf("refused refresh changed codex file set")
			}
			for path, data := range before {
				if !bytes.Equal(data, after[path]) {
					t.Fatalf("refused refresh changed %s", path)
				}
			}
			entries, err := StatusWithHome(codex, root)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, entry := range entries {
				if entry.Output == a {
					found = true
					if !strings.HasPrefix(entry.Command, "lex absorb --codex ") {
						t.Fatalf("status command = %q", entry.Command)
					}
				}
			}
			if !found {
				t.Fatalf("status has no entry for %s", a)
			}
			if err := Absorb(codex, a, AbsorbOptions{}); err != nil {
				t.Fatalf("absorb own edit: %v", err)
			}
			if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
				t.Fatalf("check after absorb: %v", err)
			}
		})
	}
}
