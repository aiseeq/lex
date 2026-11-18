package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefreshAdviceForceWhenFragmentsEdited(t *testing.T) {
	for _, tc := range []struct {
		name       string
		editedPart string
		hashPart   string
		editHeader bool
		removeHash bool
		fileHeader bool
		wantForce  bool
	}{
		{name: "own fragment edited", editedPart: "own para", wantForce: true},
		{name: "shared fragment edited", editedPart: "shared para", wantForce: true},
		{name: "fragment header edited", editedPart: "own para", editHeader: true},
		{name: "own fragment hash missing", hashPart: "own para", removeHash: true},
		{name: "shared fragment hash missing", hashPart: "shared para", removeHash: true},
		{name: "own fragment hash missing with file header", hashPart: "own para", removeHash: true, fileHeader: true},
		{name: "fragments unchanged"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, codex, outputs, _ := sharedDropFixtureWithShared(
				t,
				[]string{"own para", "shared para"},
				[]string{"shared para"},
			)
			manifestPath := filepath.Join(codex, "manifest", codexKey("docs/a.md")+".yaml")
			m := mustReadManifest(t, manifestPath)
			if tc.fileHeader {
				m.header = true
				if err := writeManifest(manifestPath, m, m.links); err != nil {
					t.Fatal(err)
				}
				if err := BuildWithOptions(codex, BuildOptions{Force: true}); err != nil {
					t.Fatalf("build with generated header: %v", err)
				}
				m = mustReadManifest(t, manifestPath)
			}
			if tc.editedPart != "" {
				id := sharedPartID(t, codex, m, tc.editedPart)
				fragmentPath := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
				raw, err := os.ReadFile(fragmentPath)
				if err != nil {
					t.Fatal(err)
				}
				body, err := fragmentBody(raw)
				if err != nil {
					t.Fatal(err)
				}
				if tc.editHeader {
					changed := bytes.Replace(raw, []byte("цель: \n"), []byte("цель: z\n"), 1)
					if bytes.Equal(changed, raw) {
						t.Fatalf("fragment header has no empty цель field")
					}
					mustWrite(t, fragmentPath, changed)
				} else {
					changed := bytes.Replace(body, []byte(tc.editedPart), []byte("edited "+tc.editedPart), 1)
					if bytes.Equal(changed, body) {
						t.Fatalf("fragment body does not contain %q", tc.editedPart)
					}
					mustWrite(t, fragmentPath, replaceFragmentBody(raw, body, changed))
				}
			}
			if tc.removeHash {
				id := sharedPartID(t, codex, m, tc.hashPart)
				delete(m.fragmentHashes, id)
				if err := writeManifest(manifestPath, m, m.links); err != nil {
					t.Fatal(err)
				}
			}

			editedFile := []byte("x\n")
			if tc.fileHeader {
				editedFile = generatedHeader(manifestPath, editedFile)
			}
			mustWrite(t, outputs["a.md"], editedFile)
			err := ImportWithOptions(codex, outputs["a.md"], ImportOptions{Refresh: true, Home: root})
			if err == nil {
				t.Fatal("refresh unexpectedly succeeded")
			}
			message := err.Error()
			if tc.wantForce {
				for _, want := range []string{"lex absorb --force --codex", "lex build --force --codex"} {
					if !strings.Contains(message, want) {
						t.Errorf("refresh refusal %q does not contain %q", message, want)
					}
				}
				if strings.Contains(message, "first delete only") {
					t.Errorf("refresh refusal unexpectedly suggests two passes: %q", message)
				}
			} else {
				if !strings.Contains(message, "first delete only") || !strings.Contains(message, "then make the other edit") {
					t.Errorf("refresh refusal lost two-pass advice: %q", message)
				}
				if strings.Contains(message, "--force") {
					t.Errorf("refresh refusal unexpectedly contains --force: %q", message)
				}
			}
		})
	}
}
