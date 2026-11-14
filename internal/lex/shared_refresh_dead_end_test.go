package lex

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runRefreshTestGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %v", args, output, err)
	}
}

func addSharedOverrideForRefreshTest(t *testing.T, codex, manifestPath, shared string) {
	t.Helper()
	m, err := readManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range m.parts {
		if id == shared {
			continue
		}
		path := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		raw = bytes.Replace(raw, []byte("цель: \n"), []byte("цель: g\n"), 1)
		raw = bytes.Replace(raw, []byte("основание: \n"), []byte("основание: o\n"), 1)
		at := bytes.Index(raw, []byte("\n---\n"))
		if at < 0 {
			t.Fatalf("fragment %s has no closing header", id)
		}
		fields := []byte("\nпереопределяет: " + shared + "\nпереопределяет_sha256: " + sha(fragmentBodyMust(t, codex, shared)))
		raw = append(append(append([]byte(nil), raw[:at]...), fields...), raw[at:]...)
		mustWrite(t, path, raw)
		m.fragmentHashes[id] = sha(raw)
		if err := writeManifest(manifestPath, m, m.links); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatal("no private fragment available for override")
}

func fragmentBodyMust(t *testing.T, codex, id string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := fragmentBody(raw)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestRefreshKeepsSharedFragmentAbsorbCannotTake(t *testing.T) {
	for _, tc := range []struct {
		name, content, second                    string
		absorb, header, detach, override, staged bool
	}{
		{name: "whole", content: "x\n", second: "z\n", detach: true},
		{name: "refresh after moving shared", content: "x\n", second: "x\n\nshared para!\n", detach: true},
		{name: "comment", content: "# a\n<!-- c -->\nown line\n", detach: true},
		{name: "fence", content: "# a\n" + string([]byte{96, 96, 96, 10}), detach: true},
		{name: "same id different body", content: "shared para!\n", detach: true},
		{name: "unshare", content: "x\nshared para EDITED\n", detach: true},
		{name: "slug shift", content: "# a\n\nshared para!\n\nshared para\n", absorb: true},
		{name: "generated header", content: "x\n", header: true, detach: true},
		{name: "override preserved", content: "x\n", detach: true, override: true},
		{name: "staged same-topic refresh", content: "x\n", detach: true, staged: true},
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
			var sharedRaw []byte
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
			if tc.override {
				addSharedOverrideForRefreshTest(t, codex, mcPath, shared)
				if err := BuildWithOptions(codex, BuildOptions{Force: true}); err != nil {
					t.Fatalf("build override fixture: %v", err)
				}
			}
			if tc.staged {
				runRefreshTestGit(t, root, "init", "-q")
				runRefreshTestGit(t, root, "add", "-A")
				runRefreshTestGit(t, root, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-qm", "initial")
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
			if tc.staged {
				runRefreshTestGit(t, root, "add", "-A", "--", "docs/a.md", filepath.Join("codex", "manifest", filepath.Base(maPath)), filepath.Join("codex", "frag", codexKey("docs/a.md")))
				t.Chdir(root)
				if _, err := CheckWithWarnings(codex, CheckOptions{Staged: true}); err != nil {
					t.Fatalf("staged check after a-only refresh: %v", err)
				}
			}
			if tc.override {
				debt, err := CodeDebt(codex)
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range debt {
					if strings.Contains(item, "lost during refresh") {
						t.Fatalf("code debt = %v", debt)
					}
				}
				raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(codexKey("docs/c.md")), "c-1.md"))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Contains(raw, []byte("переопределяет: "+shared)) {
					t.Fatalf("override header lost: %q", raw)
				}
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
			manifestBytes, err := os.ReadFile(maPath)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(manifestBytes, []byte("refresh_detached_shared")) {
				t.Fatal("manifest retained a historical detach flag")
			}
			if !tc.absorb {
				for _, id := range refreshed.parts {
					if id == shared {
						t.Fatalf("a still lists shared fragment %s", shared)
					}
				}
			}
			for _, id := range refreshed.parts {
				if !strings.HasPrefix(id, codexKey("docs/a.md")+"/") {
					t.Fatalf("a part %q is outside its topic", id)
				}
			}
			if got, _ := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(shared)+".md")); !bytes.Equal(got, sharedRaw) {
				t.Fatal("refresh or absorb changed the retained shared fragment")
			}
			if got, _ := os.ReadFile(mbPath); !bytes.Equal(got, mbBefore) {
				t.Fatal("b manifest changed")
			}
			if got, _ := os.ReadFile(mcPath); !bytes.Equal(got, mcBefore) {
				t.Fatal("c manifest changed")
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
						if strings.Contains(entry.SharedHint, "remains used by  ") {
							t.Fatalf("second status hint has empty owner list: %#v", entry)
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
				if strings.Contains(advice.Error(), "remains used by  —") || strings.Contains(advice.Error(), "used by  —") {
					t.Fatalf("second check advice has empty owner list: %v", advice)
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
				if tc.name == "refresh after moving shared" {
					m, err := readManifest(maPath)
					if err != nil {
						t.Fatal(err)
					}
					for _, id := range m.parts {
						if id == shared {
							t.Fatalf("new a fragment reused shared ID %s", shared)
						}
					}
					if got, _ := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(shared)+".md")); !bytes.Equal(got, sharedRaw) {
						t.Fatal("second refresh overwrote the shared fragment")
					}
				}
			}
		})
	}
}
