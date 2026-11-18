package lex

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefreshKeepsSharedFragmentBytes(t *testing.T) {
	t.Run("shared fragment starts with blank", func(t *testing.T) {
		root := t.TempDir()
		codex := filepath.Join(root, "codex")
		a, b := filepath.Join(root, "docs", "a.md"), filepath.Join(root, "docs", "b.md")
		if err := os.MkdirAll(filepath.Dir(a), directoryMode); err != nil {
			t.Fatal(err)
		}
		for _, item := range []struct{ path, heading string }{{a, "# A\n"}, {b, "# B\n"}} {
			mustWrite(t, item.path, []byte(item.heading))
			if err := ImportWithOptions(codex, item.path, ImportOptions{Home: root}); err != nil {
				t.Fatal(err)
			}
			mustWrite(t, item.path, []byte(item.heading+"\nshared para\n"))
			if err := Absorb(codex, item.path, AbsorbOptions{}); err != nil {
				t.Fatal(err)
			}
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
		if len(ma.parts) != 2 || len(mb.parts) != 2 {
			t.Fatalf("unexpected parts: a=%v b=%v", ma.parts, mb.parts)
		}
		shared := ma.parts[1]
		sharedPath := filepath.Join(codex, "frag", filepath.FromSlash(shared)+".md")
		before, err := os.ReadFile(sharedPath)
		if err != nil {
			t.Fatal(err)
		}
		body, err := fragmentBody(before)
		if err != nil || string(body) != "\nshared para\n" {
			t.Fatalf("shared body = %q, err %v", body, err)
		}
		oldID := mb.parts[1]
		if err := os.Remove(filepath.Join(codex, "frag", filepath.FromSlash(oldID)+".md")); err != nil {
			t.Fatal(err)
		}
		mb.parts[1] = shared
		delete(mb.fragmentHashes, oldID)
		mb.fragmentHashes[shared] = ma.fragmentHashes[shared]
		if err := writeManifest(mbPath, mb, mb.links); err != nil {
			t.Fatal(err)
		}
		if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
			t.Fatalf("initial check: %v", err)
		}
		other, err := os.ReadFile(b)
		if err != nil {
			t.Fatal(err)
		}
		want := []byte("# A edited\n\nshared para\n")
		mustWrite(t, a, want)
		err = ImportWithOptions(codex, a, ImportOptions{Refresh: true, Home: root})
		if err == nil || !strings.Contains(err.Error(), "would change a fragment shared") {
			t.Fatalf("refresh refusal = %v", err)
		}
		advice := "lex absorb --codex " + ShellQuoteArg(codex) + " " + ShellQuoteArg(a)
		if !strings.Contains(err.Error(), "`"+advice+"`") {
			t.Fatalf("refresh refusal lacks advice %q: %v", advice, err)
		}
		assertUnchanged := func() {
			t.Helper()
			for _, item := range []struct {
				path string
				want []byte
			}{{sharedPath, before}, {b, other}, {a, want}} {
				got, err := os.ReadFile(item.path)
				if err != nil || !bytes.Equal(got, item.want) {
					t.Fatalf("%s = %q, want %q: %v", item.path, got, item.want, err)
				}
			}
		}
		assertUnchanged()
		binary := filepath.Join(root, "lex")
		build := exec.Command("go", "build", "-o", binary, "./cmd/lex")
		build.Dir = "../.."
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build lex: %v\n%s", err, output)
		}
		command := strings.Replace(advice, "lex ", ShellQuoteArg(binary)+" ", 1)
		if output, err := exec.Command("sh", "-c", command).CombinedOutput(); err != nil {
			t.Fatalf("advice %s: %v\n%s", advice, err, output)
		}
		assertUnchanged()
		if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
			t.Fatalf("check after advice: %v", err)
		}
	})
	for _, tc := range []struct{ name, from, to string }{
		{"own paragraph", "own para", "own para edited"},
		{"heading", "# A", "# A edited"},
		{"new paragraph", "own para\n", "own para\n\nnew para\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, codex, a, b := sharedEditFixture(t)
			mustWrite(t, a, []byte("# A\n\nshared para\n\nown para\n"))
			if err := Absorb(codex, a, AbsorbOptions{}); err != nil {
				t.Fatal(err)
			}
			m, err := readManifest(filepath.Join(codex, "manifest", codexKey("docs/a.md")+".yaml"))
			if err != nil {
				t.Fatal(err)
			}
			var sharedPath string
			for _, id := range m.parts {
				path := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(raw), "shared para") {
					sharedPath = path
				}
			}
			before, err := os.ReadFile(sharedPath)
			if err != nil {
				t.Fatal(err)
			}
			other, err := os.ReadFile(b)
			if err != nil {
				t.Fatal(err)
			}
			want := []byte(strings.Replace("# A\n\nshared para\n\nown para\n", tc.from, tc.to, 1))
			mustWrite(t, a, want)
			if err := ImportWithOptions(codex, a, ImportOptions{Refresh: true, Home: root}); err != nil {
				t.Fatalf("refresh: %v", err)
			}
			after, err := os.ReadFile(sharedPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("shared fragment changed: before %q, after %q, err %v", before, after, err)
			}
			if err := Build(codex, false); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(a)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("built a = %q, want %q: %v", got, want, err)
			}
			got, err = os.ReadFile(b)
			if err != nil || !bytes.Equal(got, other) {
				t.Fatalf("built b = %q, want %q: %v", got, other, err)
			}
			if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
				t.Fatalf("check: %v", err)
			}
		})
	}
}
