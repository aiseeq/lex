package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func adviceSharedPurposeFixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	root, codex, a, b := sharedEditFixture(t)
	purposeSet(t, codex, true)
	maPath := filepath.Join(codex, "manifest", codexKey("docs/a.md")+".yaml")
	mbPath := filepath.Join(codex, "manifest", codexKey("docs/b.md")+".yaml")
	ma := mustReadManifest(t, maPath)
	mb := mustReadManifest(t, mbPath)
	shared := ma.parts[1]
	fragmentPath := filepath.Join(codex, "frag", filepath.FromSlash(shared)+".md")
	raw, err := os.ReadFile(fragmentPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("цель: \n"), []byte("цель: why\n"), 1)
	mustWrite(t, fragmentPath, raw)
	for _, manifest := range []struct {
		path string
		m    manifest
	}{{maPath, ma}, {mbPath, mb}} {
		manifest.m.fragmentHashes[shared] = sha(raw)
		if err := writeManifest(manifest.path, manifest.m, manifest.m.links); err != nil {
			t.Fatal(err)
		}
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	return root, codex, a, b
}

func TestAdviceSharedGoalLineNamesRestore(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func([]byte) []byte
	}{{
		name: "edited",
		edit: func(data []byte) []byte {
			return bytes.Replace(data, []byte("  Зачем: why\n"), []byte("  Зачем: changed\n"), 1)
		},
	}, {
		name: "deleted",
		edit: func(data []byte) []byte {
			return bytes.Replace(data, []byte("  Зачем: why\n"), nil, 1)
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			root, codex, a, b := adviceSharedPurposeFixture(t)
			data, err := os.ReadFile(a)
			if err != nil {
				t.Fatal(err)
			}
			mustWrite(t, a, tc.edit(data))
			_, checkErr := CheckWithWarnings(codex, CheckOptions{})
			if checkErr == nil || !strings.Contains(checkErr.Error(), `"  Зачем: why"`) || !strings.Contains(checkErr.Error(), "put the line") || !strings.Contains(checkErr.Error(), "back") || strings.Contains(checkErr.Error(), "--shared") {
				t.Fatalf("check advice should explain restoring the goal line without --shared: %v", checkErr)
			}
			entries, err := StatusWithHome(codex, root)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Output != a {
					continue
				}
				if !strings.Contains(entry.Refusal, `"  Зачем: why"`) || !strings.Contains(entry.Refusal, "put the line") || !strings.Contains(entry.Refusal, "back") || strings.Contains(entry.Command, "--shared") || strings.Contains(entry.SharedHint, b) {
					t.Fatalf("status advice should explain restoring the goal line without --shared: %#v", entry)
				}
				return
			}
			t.Fatalf("status has no entry for %s", a)
		})
	}
}

func TestCheckStagedAdviceNamesManifestMissingOnDisk(t *testing.T) {
	main, _, codex, git := codexInsideProjectWorktree(t)
	out := filepath.Join(main, "CLAUDE.md")
	manifestPaths, err := filepath.Glob(filepath.Join(codex, "manifest", "*.yaml"))
	if err != nil || len(manifestPaths) != 1 {
		t.Fatalf("find manifest: %v, %v", manifestPaths, err)
	}
	manifestPath := manifestPaths[0]
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, []byte(manualInstruction(string(data))), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := git(main, "add", filepath.Base(out)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(manifestPath); err != nil {
		t.Fatal(err)
	}
	t.Chdir(main)
	_, err = CheckWithWarnings(codex, CheckOptions{Staged: true})
	if err == nil || !strings.Contains(err.Error(), manifestPath) || !strings.Contains(strings.ToLower(err.Error()), "restore") || strings.Contains(err.Error(), "lex absorb") {
		t.Fatalf("staged advice should name and restore missing-on-disk manifest %s, got %v", manifestPath, err)
	}
}
