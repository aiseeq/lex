package lex

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func findPartWithBody(t *testing.T, codex string, m manifest, want []byte) string {
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
		if bytes.Equal(body, want) {
			return id
		}
	}
	t.Fatalf("no fragment with body %q in %v", want, m.parts)
	return ""
}

func TestRefreshKeepsSharedFragmentAbsorbCannotTake(t *testing.T) {
	treeAdvice := treeOutputAdvice("out.md: differs — run `lex import --refresh --codex c out.md`", "out.md", "tree/out.md", false)
	if !strings.Contains(treeAdvice, "before running `lex import --refresh --codex c out.md`") {
		t.Fatalf("tree refresh advice = %q", treeAdvice)
	}
	for _, tc := range []struct {
		name, content, second  string
		absorb, header, detach bool
	}{
		{name: "whole", content: "x\n", second: "z\n", detach: true},
		{name: "refresh after moving shared", content: "x\n", second: "x\n\nshared para!\n", detach: true},
		{name: "comment", content: "# a\n<!-- c -->\nown line\n", detach: true},
		{name: "fence", content: "# a\n" + string([]byte{96, 96, 96, 10}), detach: true},
		{name: "same id different body", content: "shared para!\n", detach: true},
		{name: "unshare", content: "x\nshared para EDITED\n", detach: true},
		{name: "slug shift", content: "# a\n\nshared para!\n\nshared para\n", absorb: true},
		{name: "generated header", content: "x\n", header: true, detach: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, codex, a, b := sharedEditFixture(t)
			c := filepath.Join(root, "docs", "c.md")
			mustWrite(t, c, []byte("# C\n\nshared para\n"))
			if err := ImportWithOptions(codex, c, ImportOptions{Home: root}); err != nil {
				t.Fatal(err)
			}
			maPath := filepath.Join(codex, "manifest", codexKey("docs/a.md")+".yaml")
			mbPath := filepath.Join(codex, "manifest", codexKey("docs/b.md")+".yaml")
			mcPath := filepath.Join(codex, "manifest", codexKey("docs/c.md")+".yaml")
			if tc.header {
				if err := setManifestHeader(codex, "docs/a.md", true); err != nil {
					t.Fatal(err)
				}
				if err := Build(codex, false); err != nil {
					t.Fatal(err)
				}
			}
			ma, err := readManifest(maPath)
			if err != nil {
				t.Fatal(err)
			}
			shared := ""
			var sharedBody, sharedRaw []byte
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
					sharedBody = append([]byte(nil), body...)
					sharedRaw = append([]byte(nil), raw...)
				}
			}
			mc, err := readManifest(mcPath)
			if err != nil {
				t.Fatal(err)
			}
			for i, id := range mc.parts {
				raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
				if err != nil {
					t.Fatal(err)
				}
				body, err := fragmentBody(raw)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Equal(body, []byte("shared para\n")) {
					delete(mc.fragmentHashes, id)
					mc.parts[i] = shared
					mc.fragmentHashes[shared] = ma.fragmentHashes[shared]
					if err := os.Remove(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")); err != nil {
						t.Fatal(err)
					}
					break
				}
			}
			if err := writeManifest(mcPath, mc, mc.links); err != nil {
				t.Fatal(err)
			}
			beforeB, _ := os.ReadFile(b)
			beforeC, _ := os.ReadFile(c)
			mbBefore, _ := os.ReadFile(mbPath)
			mcBefore, _ := os.ReadFile(mcPath)
			sourceWant := []byte(tc.content)
			if tc.header {
				sourceWant = generatedHeader(maPath, sourceWant)
			}
			mustWrite(t, a, sourceWant)
			entries, err := StatusWithHome(codex, root)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, entry := range entries {
				if entry.Output == a {
					found = true
					wantCommand := "lex import --refresh "
					if tc.absorb {
						wantCommand = "lex absorb "
					}
					if !strings.HasPrefix(entry.Command, wantCommand) {
						t.Fatalf("status command = %q", entry.Command)
					}
					if tc.detach && (!strings.Contains(entry.SharedHint, b) || !strings.Contains(entry.SharedHint, c)) {
						t.Fatalf("status does not explain detach: %#v", entry)
					}
				}
			}
			if !found {
				t.Fatal("status has no entry")
			}
			_, checkAdvice := CheckWithWarnings(codex, CheckOptions{})
			wantAdvice := "lex import --refresh"
			if tc.absorb {
				wantAdvice = "lex absorb"
			}
			if checkAdvice == nil || !strings.Contains(checkAdvice.Error(), wantAdvice) {
				t.Fatalf("check advice = %v", checkAdvice)
			}
			if tc.detach && (!strings.Contains(checkAdvice.Error(), b) || !strings.Contains(checkAdvice.Error(), c)) {
				t.Fatalf("check does not explain detach: %v", checkAdvice)
			}
			if tc.absorb {
				if err := Absorb(codex, a, AbsorbOptions{}); err != nil {
					t.Fatalf("absorb: %v", err)
				}
			} else if tc.detach {
				oldStderr := os.Stderr
				readEnd, writeEnd, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				os.Stderr = writeEnd
				err = ImportWithOptions(codex, a, ImportOptions{Refresh: true, Home: root})
				_ = writeEnd.Close()
				os.Stderr = oldStderr
				warning, readErr := io.ReadAll(readEnd)
				_ = readEnd.Close()
				if err != nil {
					t.Fatalf("refresh: %v", err)
				}
				if readErr != nil {
					t.Fatal(readErr)
				}
				if !strings.Contains(string(warning), b) || !strings.Contains(string(warning), c) {
					t.Fatalf("refresh warning = %q", warning)
				}
			} else if err := ImportWithOptions(codex, a, ImportOptions{Refresh: true, Home: root}); err != nil {
				t.Fatalf("refresh: %v", err)
			}
			if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
				t.Fatalf("check: %v", err)
			}
			if got, _ := os.ReadFile(b); !bytes.Equal(got, beforeB) {
				t.Fatal("b changed")
			}
			if got, _ := os.ReadFile(c); !bytes.Equal(got, beforeC) {
				t.Fatal("c changed")
			}

			refreshed, err := readManifest(maPath)
			if err != nil {
				t.Fatal(err)
			}
			if tc.absorb {
				if got, _ := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(shared)+".md")); !bytes.Equal(got, sharedRaw) {
					t.Fatal("absorb changed shared fragment")
				}
				if got, _ := os.ReadFile(mbPath); !bytes.Equal(got, mbBefore) {
					t.Fatal("absorb changed b manifest")
				}
				if got, _ := os.ReadFile(mcPath); !bytes.Equal(got, mcBefore) {
					t.Fatal("absorb changed c manifest")
				}
			} else {
				movedB, err := readManifest(mbPath)
				if err != nil {
					t.Fatal(err)
				}
				movedC, err := readManifest(mcPath)
				if err != nil {
					t.Fatal(err)
				}
				moved := findPartWithBody(t, codex, movedB, sharedBody)
				if moved == shared || !strings.HasPrefix(moved, codexKey("docs/b.md")+"/") {
					t.Fatalf("shared ID = %q, want relocated to b topic", moved)
				}
				if !slices.Contains(movedC.parts, moved) {
					t.Fatalf("c parts %v omit relocated %s", movedC.parts, moved)
				}
				raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(moved)+".md"))
				if err != nil {
					t.Fatal(err)
				}
				body, err := fragmentBody(raw)
				if err != nil || !bytes.Equal(body, sharedBody) {
					t.Fatalf("relocated shared body = %q, %v", body, err)
				}
				if movedB.fragmentHashes[moved] != sha(raw) || movedC.fragmentHashes[moved] != sha(raw) {
					t.Fatal("relocated shared fragment hashes are incorrect")
				}
				for _, id := range refreshed.parts {
					if id == moved {
						t.Fatalf("a still lists relocated shared fragment %s", moved)
					}
					if !strings.HasPrefix(id, codexKey("docs/a.md")+"/") {
						t.Fatalf("a part %q is outside its topic", id)
					}
				}
				if _, err := os.Stat(filepath.Join(codex, "frag", filepath.FromSlash(shared)+".md")); !os.IsNotExist(err) {
					t.Fatalf("old shared fragment remains: %v", err)
				}
			}
			if got, _ := os.ReadFile(a); !bytes.Equal(got, sourceWant) {
				t.Fatalf("a changed: %q", got)
			}
			if tc.second != "" {
				mustWrite(t, a, []byte(tc.second))
				entries, err := StatusWithHome(codex, root)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, entry := range entries {
					if entry.Output == a {
						found = true
						if !strings.HasPrefix(entry.Command, "lex import --refresh ") {
							t.Fatalf("second status command = %q", entry.Command)
						}
					}
				}
				if !found {
					t.Fatal("status omitted second edit")
				}
				_, advice := CheckWithWarnings(codex, CheckOptions{})
				if advice == nil || !strings.Contains(advice.Error(), "lex import --refresh") {
					t.Fatalf("second check advice = %v", advice)
				}
				if err := ImportWithOptions(codex, a, ImportOptions{Refresh: true, Home: root}); err != nil {
					t.Fatalf("second refresh: %v", err)
				}
				if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
					t.Fatalf("check after second refresh: %v", err)
				}
				if got, _ := os.ReadFile(b); !bytes.Equal(got, beforeB) {
					t.Fatal("second refresh changed b")
				}
				if got, _ := os.ReadFile(c); !bytes.Equal(got, beforeC) {
					t.Fatal("second refresh changed c")
				}
			}
		})
	}
}
