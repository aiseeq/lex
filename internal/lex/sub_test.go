package lex

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type subFrag struct{ id, goal, body string }

// subFixture writes a codex with the given fragments and outputs (output name → part ids), prints purposes when
// purpose is set and builds every output. It returns the codex and the output paths by name.
func subFixture(t *testing.T, purpose bool, frags []subFrag, outs map[string][]string) (string, map[string]string) {
	t.Helper()
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	if err := os.MkdirAll(filepath.Join(codex, "manifest"), directoryMode); err != nil {
		t.Fatal(err)
	}
	if purpose {
		mustWrite(t, filepath.Join(codex, "purpose.json"), []byte(`{"print": true}`))
	}
	hashes := map[string]string{}
	for _, f := range frags {
		raw := "---\nid: " + f.id + "\nцель: " + f.goal + "\nоснование: тест\nснять_когда: тест\n---\n" + f.body
		path := filepath.Join(codex, "frag", filepath.FromSlash(f.id)+".md")
		if err := os.MkdirAll(filepath.Dir(path), directoryMode); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, path, []byte(raw))
		hashes[f.id] = sha([]byte(raw))
	}
	paths := map[string]string{}
	for name, parts := range outs {
		out := filepath.Join(root, "out", name)
		if err := os.MkdirAll(filepath.Dir(out), directoryMode); err != nil {
			t.Fatal(err)
		}
		m := manifest{out: out, hash: "fixture", parts: parts, fragmentHashes: map[string]string{}, header: true}
		for _, id := range parts {
			m.fragmentHashes[id] = hashes[id]
		}
		if err := writeManifest(filepath.Join(codex, "manifest", codexKey(name)+".yaml"), m, nil); err != nil {
			t.Fatal(err)
		}
		if err := BuildWithOptions(codex, BuildOptions{Force: true, Output: out}); err != nil {
			t.Fatal(err)
		}
		paths[name] = out
	}
	return codex, paths
}

func fragText(t *testing.T, codex, id string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// subExpect runs Sub and checks the success invariants: the file equals the direct replacement and the codex builds clean.
func subExpect(t *testing.T, codex, file string, edits ...SubEdit) SubResult {
	t.Helper()
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	want := before
	for _, e := range edits {
		want = bytes.ReplaceAll(want, []byte(e.Old), []byte(e.New))
	}
	result, err := Sub(codex, file, edits)
	if err != nil {
		t.Fatalf("Sub: %v", err)
	}
	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, want) {
		t.Fatalf("file after Sub:\n%s\nwant:\n%s", after, want)
	}
	if err := Build(codex, true); err != nil {
		t.Fatalf("build --check after Sub: %v", err)
	}
	return result
}

// subRefused runs Sub, expects a refusal and an untouched tree; it returns the error.
func subRefused(t *testing.T, codex, file string, edits ...SubEdit) error {
	t.Helper()
	root := filepath.Dir(codex)
	before := treeBytes(t, root)
	_, err := Sub(codex, file, edits)
	if err == nil {
		t.Fatal("Sub succeeded, want a refusal")
	}
	if !bytes.Equal(before, treeBytes(t, root)) {
		t.Fatalf("refused Sub changed files: %v", err)
	}
	return err
}

func one(old, repl string) SubEdit { return SubEdit{Old: old, New: repl, Want: 1, Origin: "arguments"} }

func TestSubEditsFragmentBody(t *testing.T) {
	codex, outs := subFixture(t, true, []subFrag{
		{"a/one", "первая цель", "- пункт один\n"},
		{"a/two", "вторая цель", "- пункт два\n"},
	}, map[string][]string{"a.md": {"a/one", "a/two"}})
	result := subExpect(t, codex, outs["a.md"], one("пункт два", "пункт номер два"))
	if !strings.HasSuffix(fragText(t, codex, "a/two"), "\n---\n- пункт номер два\n") {
		t.Fatalf("fragment two = %q", fragText(t, codex, "a/two"))
	}
	if len(result.Shared) != 0 || len(result.Changed) != 2 {
		t.Fatalf("result = %+v", result)
	}
	m, err := readManifest(filepath.Join(codex, "manifest", codexKey("a.md")+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	built, _ := os.ReadFile(outs["a.md"])
	if m.builtHash != sha(built) || m.fragmentHashes["a/two"] != sha([]byte(fragText(t, codex, "a/two"))) {
		t.Fatal("manifest hashes not updated")
	}
}

func TestSubPurposeLineEditsGoalField(t *testing.T) {
	codex, outs := subFixture(t, true, []subFrag{
		{"a/one", "первая цель", "- пункт один\n"},
		{"a/two", "вторая цель", "- пункт два\n"},
	}, map[string][]string{"a.md": {"a/one", "a/two"}})
	subExpect(t, codex, outs["a.md"], one("вторая цель", "другая цель"))
	text := fragText(t, codex, "a/two")
	if !strings.Contains(text, "\nцель: другая цель\n") || !strings.HasSuffix(text, "---\n- пункт два\n") {
		t.Fatalf("fragment two = %q", text)
	}
	// A line break in the «Зачем:» line cannot live in the one-line field.
	err := subRefused(t, codex, outs["a.md"], one("другая цель", "другая\nцель"))
	var refusal *SubRefusal
	if !errors.As(err, &refusal) || !strings.Contains(err.Error(), "цель") {
		t.Fatalf("refusal = %v", err)
	}
	// A goal that ends in a space would be trimmed by the build: rebuild differs, nothing written.
	subRefused(t, codex, outs["a.md"], one("другая цель", "другая цель "))
}

func TestSubRefusesHeaderLine(t *testing.T) {
	codex, outs := subFixture(t, true, []subFrag{{"a/one", "цель", "- пункт один\n"}}, map[string][]string{"a.md": {"a/one"}})
	err := subRefused(t, codex, outs["a.md"], one("собран из", "собрано из"))
	if !strings.Contains(err.Error(), "lex header") {
		t.Fatalf("refusal = %v", err)
	}
	// The match may include the header when the change itself is below it.
	subExpect(t, codex, outs["a.md"], one("-->\n- пункт один", "-->\n- пункт первый"))
}

func TestSubAcrossFragmentBoundary(t *testing.T) {
	t.Run("two bodies", func(t *testing.T) {
		codex, outs := subFixture(t, false, []subFrag{
			{"a/one", "", "- пункт один\n"},
			{"a/two", "", "- пункт два\n"},
		}, map[string][]string{"a.md": {"a/one", "a/two"}})
		subExpect(t, codex, outs["a.md"], one("один\n- пункт два", "1\n- пункт 2"))
		if !strings.HasSuffix(fragText(t, codex, "a/one"), "---\n- пункт 1\n") || !strings.HasSuffix(fragText(t, codex, "a/two"), "---\n- пункт 2\n") {
			t.Fatalf("fragments = %q, %q", fragText(t, codex, "a/one"), fragText(t, codex, "a/two"))
		}
	})
	t.Run("body and goal around the marker", func(t *testing.T) {
		codex, outs := subFixture(t, true, []subFrag{
			{"a/one", "первая цель", "- пункт один\n"},
			{"a/two", "вторая цель", "- пункт два\n"},
		}, map[string][]string{"a.md": {"a/one", "a/two"}})
		subExpect(t, codex, outs["a.md"], one("один\n  Зачем: первая", "1\n  Зачем: 1-я"))
		text := fragText(t, codex, "a/one")
		if !strings.Contains(text, "\nцель: 1-я цель\n") || !strings.HasSuffix(text, "---\n- пункт 1\n") {
			t.Fatalf("fragment one = %q", text)
		}
	})
	t.Run("goal and next body", func(t *testing.T) {
		codex, outs := subFixture(t, true, []subFrag{
			{"a/one", "первая цель", "- пункт один\n"},
			{"a/two", "вторая цель", "- пункт два\n"},
		}, map[string][]string{"a.md": {"a/one", "a/two"}})
		subExpect(t, codex, outs["a.md"], one("первая цель\n- пункт два", "цель 1\n- пункт 2"))
		if !strings.Contains(fragText(t, codex, "a/one"), "\nцель: цель 1\n") || !strings.HasSuffix(fragText(t, codex, "a/two"), "---\n- пункт 2\n") {
			t.Fatalf("fragments = %q, %q", fragText(t, codex, "a/one"), fragText(t, codex, "a/two"))
		}
	})
	t.Run("change of the marker is refused", func(t *testing.T) {
		codex, outs := subFixture(t, true, []subFrag{
			{"a/one", "первая цель", "- пункт один\n"},
			{"a/two", "вторая цель", "- пункт два\n"},
		}, map[string][]string{"a.md": {"a/one", "a/two"}})
		err := subRefused(t, codex, outs["a.md"], one("один\n  Зачем: первая", "1\n  Why: первая"))
		if !strings.Contains(err.Error(), "Зачем") {
			t.Fatalf("refusal = %v", err)
		}
	})
	t.Run("boundary edit that does not rebuild is refused", func(t *testing.T) {
		codex, outs := subFixture(t, true, []subFrag{
			{"a/one", "первая цель", "- пункт один\n"},
			{"a/two", "вторая цель", "- пункт два\n"},
		}, map[string][]string{"a.md": {"a/one", "a/two"}})
		// Joining the purpose line with the next item: the build would put the «Зачем:» line elsewhere.
		subRefused(t, codex, outs["a.md"], one("первая цель\n- пункт два", "первая цель - пункт два"))
	})
}

func TestSubSharedFragmentRebuildsOtherOutputs(t *testing.T) {
	frags := []subFrag{
		{"a/own", "", "# A\n"},
		{"b/own", "", "# B\n"},
		{"common/shared", "общая цель", "- общий пункт\n"},
	}
	outs := map[string][]string{"a.md": {"a/own", "common/shared"}, "b.md": {"b/own", "common/shared"}}
	t.Run("rebuilds", func(t *testing.T) {
		codex, paths := subFixture(t, true, frags, outs)
		result := subExpect(t, codex, paths["a.md"], one("общий пункт", "общий пункт ЖЖ"))
		if !slices.Equal(result.Shared, []string{paths["b.md"]}) {
			t.Fatalf("shared = %v", result.Shared)
		}
		other, _ := os.ReadFile(paths["b.md"])
		if !strings.Contains(string(other), "общий пункт ЖЖ") {
			t.Fatalf("other output = %q", other)
		}
		if len(result.Changed) != 3 {
			t.Fatalf("changed = %v", result.Changed)
		}
	})
	t.Run("refused when the other output has unabsorbed edits", func(t *testing.T) {
		codex, paths := subFixture(t, true, frags, outs)
		data, _ := os.ReadFile(paths["b.md"])
		mustWrite(t, paths["b.md"], append(data, []byte("ручная правка\n")...))
		err := subRefused(t, codex, paths["a.md"], one("общий пункт", "общий пункт ЖЖ"))
		if !strings.Contains(err.Error(), paths["b.md"]) {
			t.Fatalf("refusal = %v", err)
		}
	})
	t.Run("rollback when a write fails", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("root writes read-only files")
		}
		codex, paths := subFixture(t, true, frags, outs)
		if err := os.Chmod(paths["b.md"], 0o444); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(paths["b.md"], fileMode)
		subRefused(t, codex, paths["a.md"], one("общий пункт", "общий пункт ЖЖ"))
	})
}

func TestSubSpecAndCount(t *testing.T) {
	codex, outs := subFixture(t, true, []subFrag{
		{"a/one", "цель один", "- слово и слово\n"},
		{"a/two", "цель два", "- слово тут\n"},
	}, map[string][]string{"a.md": {"a/one", "a/two"}})
	file := outs["a.md"]
	spec := "<<<<<<< SEARCH 3\nслово\n=======\nтермин\n>>>>>>> REPLACE\n" + file + "\n<<<<<<< SEARCH\n- термин тут\n=======\n- термин здесь\n>>>>>>> REPLACE\n"
	edits, err := ParseSubSpec(strings.NewReader(spec), file)
	if err != nil {
		t.Fatal(err)
	}
	if len(edits) != 2 || edits[0].Want != 3 || edits[1].Old != "- термин тут" {
		t.Fatalf("edits = %+v", edits)
	}
	subExpect(t, codex, file, edits...)
	if !strings.HasSuffix(fragText(t, codex, "a/one"), "---\n- термин и термин\n") || !strings.HasSuffix(fragText(t, codex, "a/two"), "---\n- термин здесь\n") {
		t.Fatalf("fragments = %q, %q", fragText(t, codex, "a/one"), fragText(t, codex, "a/two"))
	}
	err = subRefused(t, codex, file, SubEdit{Old: "термин", New: "слово", Want: 2, Origin: "arguments"})
	var count *SubCountError
	if !errors.As(err, &count) || count.Found != 3 || count.Want != 2 {
		t.Fatalf("count refusal = %v", err)
	}
	// All or nothing: the second block fails, the first is not written either.
	subRefused(t, codex, file, one("- термин здесь", "- x"), one("нет такого", "y"))
	if _, err := ParseSubSpec(strings.NewReader("other.md\n<<<<<<< SEARCH\na\n=======\nb\n>>>>>>> REPLACE\n"), file); err == nil {
		t.Fatal("spec for another file accepted")
	}
}

func TestSubRefusesFileThatDiffersFromBuild(t *testing.T) {
	codex, outs := subFixture(t, true, []subFrag{{"a/one", "цель", "- пункт один\n"}}, map[string][]string{"a.md": {"a/one"}})
	data, _ := os.ReadFile(outs["a.md"])
	mustWrite(t, outs["a.md"], append(data, []byte("- ручной пункт\n")...))
	err := subRefused(t, codex, outs["a.md"], one("пункт один", "пункт 1"))
	if !strings.Contains(err.Error(), "differs from its build") {
		t.Fatalf("refusal = %v", err)
	}
}

func TestSubInsertionAtFragmentEdges(t *testing.T) {
	codex, outs := subFixture(t, true, []subFrag{
		{"a/one", "первая цель", "- пункт один\n"},
		{"a/two", "вторая цель", "- пункт два\n"},
	}, map[string][]string{"a.md": {"a/one", "a/two"}})
	// A new item between two fragments would land in one of them with its fields: refused, absorb makes a fragment.
	err := subRefused(t, codex, outs["a.md"], one("первая цель\n- пункт два", "первая цель\n- новый пункт\n- пункт два"))
	if !strings.Contains(err.Error(), "structure of fragment") {
		t.Fatalf("refusal = %v", err)
	}
	// A continuation line after the item text stays above the «Зачем:» line, in the same fragment.
	subExpect(t, codex, outs["a.md"], one("- пункт один\n", "- пункт один\n  продолжение\n"))
	if !strings.HasSuffix(fragText(t, codex, "a/one"), "---\n- пункт один\n  продолжение\n") {
		t.Fatalf("fragment one = %q", fragText(t, codex, "a/one"))
	}
	// A nested sub-item is wording of its item, as absorb keeps it: not a structural edit.
	subExpect(t, codex, outs["a.md"], one("  продолжение\n", "  продолжение\n  - подпункт\n  1. шаг\n"))
	if !strings.HasSuffix(fragText(t, codex, "a/one"), "---\n- пункт один\n  продолжение\n  - подпункт\n  1. шаг\n") {
		t.Fatalf("fragment one = %q", fragText(t, codex, "a/one"))
	}
}

// Edits that change what a fragment is, not its wording, are refused: lex sub keeps each fragment's fields.
func TestSubRefusesStructuralEdits(t *testing.T) {
	t.Run("new item", func(t *testing.T) {
		codex, outs := subFixture(t, true, []subFrag{
			{"a/one", "первая цель", "- без ведома пользователя\n"},
			{"a/two", "вторая цель", "- Вопрос любому человеку\n"},
		}, map[string][]string{"a.md": {"a/one", "a/two"}})
		err := subRefused(t, codex, outs["a.md"], one("первая цель\n- Вопрос любому", "первая цель\n- Новый пункт ревью.\n- Вопрос любому"))
		if !strings.Contains(err.Error(), "structure of fragment") {
			t.Fatalf("refusal = %v", err)
		}
	})
	t.Run("intro paragraph after a heading", func(t *testing.T) {
		codex, outs := subFixture(t, true, []subFrag{
			{"a/head", "", "# Раздел\n"},
			{"a/item", "цель пункта", "- пункт\n"},
		}, map[string][]string{"a.md": {"a/head", "a/item"}})
		err := subRefused(t, codex, outs["a.md"], one("# Раздел\n- пункт", "# Раздел\nВводный абзац раздела.\n- пункт"))
		if !strings.Contains(err.Error(), "structure of fragment") {
			t.Fatalf("refusal = %v", err)
		}
	})
	t.Run("continuation of a fragment without purpose", func(t *testing.T) {
		codex, outs := subFixture(t, true, []subFrag{
			{"a/one", "", "- пункт без цели\n"},
			{"a/two", "вторая цель", "- пункт два\n"},
		}, map[string][]string{"a.md": {"a/one", "a/two"}})
		err := subRefused(t, codex, outs["a.md"], one("- пункт без цели\n", "- пункт без цели\n  продолжение строки\n"))
		if !strings.Contains(err.Error(), "structure of fragment") {
			t.Fatalf("refusal = %v", err)
		}
	})
	t.Run("swap of two items", func(t *testing.T) {
		codex, outs := subFixture(t, true, []subFrag{
			{"a/one", "", "- альфа\n"},
			{"a/two", "", "- бета\n"},
		}, map[string][]string{"a.md": {"a/one", "a/two"}})
		err := subRefused(t, codex, outs["a.md"], one("- альфа\n- бета\n", "- бета\n- альфа\n"))
		if !strings.Contains(err.Error(), "words of fragment") {
			t.Fatalf("refusal = %v", err)
		}
	})
	t.Run("swap of two items with purposes", func(t *testing.T) {
		codex, outs := subFixture(t, true, []subFrag{
			{"a/one", "цель альфы", "- альфа длинный пункт\n"},
			{"a/two", "цель беты", "- бета другой пункт\n"},
		}, map[string][]string{"a.md": {"a/one", "a/two"}})
		err := subRefused(t, codex, outs["a.md"], one("- альфа длинный пункт\n  Зачем: цель альфы\n- бета другой пункт\n  Зачем: цель беты\n",
			"- бета другой пункт\n  Зачем: цель беты\n- альфа длинный пункт\n  Зачем: цель альфы\n"))
		if !strings.Contains(err.Error(), "words of fragment") {
			t.Fatalf("refusal = %v", err)
		}
	})
	t.Run("same word in two fragments is not a move", func(t *testing.T) {
		codex, outs := subFixture(t, true, []subFrag{
			{"a/one", "", "- старое слово тут\n"},
			{"a/two", "", "- старое слово там\n"},
		}, map[string][]string{"a.md": {"a/one", "a/two"}})
		subExpect(t, codex, outs["a.md"], SubEdit{Old: "старое", New: "новое", Want: 2, Origin: "arguments"})
	})
}
