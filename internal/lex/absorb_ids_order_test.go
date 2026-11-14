package lex

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAbsorbNewFragmentIDsFollowFileOrder(t *testing.T) {
	root := t.TempDir()
	for run := range 100 {
		runDir := filepath.Join(root, fmt.Sprintf("run-%03d", run))
		codex := filepath.Join(runDir, "codex")
		out := filepath.Join(runDir, "docs", "g.md")
		if err := importData(codex, out, "docs/g.md", []byte("# T\n\n- one\n\n- two\n\n- three\n\n"), false); err != nil {
			t.Fatalf("run %d: import: %v", run, err)
		}
		manifestPath := filepath.Join(codex, "manifest", codexKey("docs/g.md")+".yaml")
		m := mustReadManifest(t, manifestPath)
		parts := []string{m.parts[0], m.parts[1], m.parts[2], m.parts[1]}
		parts = append(parts, m.parts[3:]...)
		m.parts = parts
		if err := writeManifest(manifestPath, m, m.links); err != nil {
			t.Fatalf("run %d: write repeated manifest: %v", run, err)
		}
		if err := Build(codex, false); err != nil {
			t.Fatalf("run %d: build repeated manifest: %v", run, err)
		}

		built, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("run %d: read built output: %v", run, err)
		}
		built = bytes.ReplaceAll(built, []byte("- one\n"), []byte("- new\n- one\n"))
		mustWrite(t, out, built)
		if err := Absorb(codex, out, AbsorbOptions{DraftDir: codex}); err != nil {
			t.Fatalf("run %d: absorb: %v", run, err)
		}

		m = mustReadManifest(t, manifestPath)
		var newIDs []string
		for _, id := range m.parts {
			if strings.HasSuffix(id, "/new-1") || strings.HasSuffix(id, "/new-2") {
				if body := readFragmentBody(t, codex, id); bytes.Equal(body, []byte("- new\n")) {
					newIDs = append(newIDs, id)
				}
			}
		}
		if len(newIDs) != 2 || !strings.HasSuffix(newIDs[0], "/new-1") || !strings.HasSuffix(newIDs[1], "/new-2") {
			t.Fatalf("run %d: new fragments in file order = %v, want new-1 then new-2", run, newIDs)
		}
	}
}
