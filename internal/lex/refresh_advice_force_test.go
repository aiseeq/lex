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
		wantForce  bool
	}{
		{name: "own fragment edited", editedPart: "own para", wantForce: true},
		{name: "shared fragment edited", editedPart: "shared para", wantForce: true},
		{name: "fragments unchanged"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, codex, outputs, _ := sharedDropFixtureWithShared(
				t,
				[]string{"own para", "shared para"},
				[]string{"shared para"},
			)
			if tc.editedPart != "" {
				m := mustReadManifest(t, filepath.Join(codex, "manifest", codexKey("docs/a.md")+".yaml"))
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
				changed := bytes.Replace(body, []byte(tc.editedPart), []byte("edited "+tc.editedPart), 1)
				if bytes.Equal(changed, body) {
					t.Fatalf("fragment body does not contain %q", tc.editedPart)
				}
				mustWrite(t, fragmentPath, replaceFragmentBody(raw, body, changed))
			}

			mustWrite(t, outputs["a.md"], []byte("x\n"))
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
