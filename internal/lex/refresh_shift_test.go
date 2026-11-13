package lex

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func refreshShiftFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root, codex, output := quoteFixture(t, "- Правило один (пользователь 03.02: «первое»).\n- Правило два (пользователь 03.02: «второе»).\n")
	if err := QuotesToBasis(codex, QuotesToBasisOptions{Apply: true, Report: filepath.Join(root, "report.json")}); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, output, ImportOptions{Home: root, Refresh: true}); err != nil {
		t.Fatal(err)
	}
	return root, codex, output
}

func checkRefreshShift(t *testing.T, source string, wants map[string]string) {
	t.Helper()
	root, codex, output := refreshShiftFixture(t)
	if err := os.WriteFile(output, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, output, ImportOptions{Home: root, Refresh: true}); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(codex, "frag", "*", "*.md"))
	if err != nil || len(paths) != len(wants) {
		t.Fatalf("fragments = %v, %v", paths, err)
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := fragmentBody(raw)
		if err != nil {
			t.Fatal(err)
		}
		want, ok := wants[string(body)]
		if !ok || !strings.Contains(string(fragmentHeader(raw)), "основание: "+want+"\n") {
			t.Errorf("unexpected header for body %q: %s", body, raw)
		}
	}
}

func TestRefreshShiftInsertAbove(t *testing.T) {
	checkRefreshShift(t, "- Новое правило.\n- Правило один.\n- Правило два.\n", map[string]string{
		"- Новое правило.\n": "", "- Правило один.\n": "(пользователь 03.02: «первое»)", "- Правило два.\n": "(пользователь 03.02: «второе»)",
	})
}

func TestRefreshShiftInsertBetween(t *testing.T) {
	checkRefreshShift(t, "- Правило один.\n- Новое правило.\n- Правило два.\n", map[string]string{
		"- Новое правило.\n": "", "- Правило один.\n": "(пользователь 03.02: «первое»)", "- Правило два.\n": "(пользователь 03.02: «второе»)",
	})
}

func TestRefreshShiftInsertAndEdit(t *testing.T) {
	checkRefreshShift(t, "- Новое правило.\n- Правило один уточнено.\n- Правило два.\n", map[string]string{
		"- Новое правило.\n": "", "- Правило один уточнено.\n": "", "- Правило два.\n": "(пользователь 03.02: «второе»)",
	})
}

func TestRefreshShiftUnpairedWarning(t *testing.T) {
	root, codex, output := quoteFixture(t, "- Правило.\n")
	manifestPath := filepath.Join(codex, "manifest", codexKey("CLAUDE.md")+".yaml")
	m, err := readManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), "цель: \n", "цель: Сохранить смысл\n", 1))
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	m.fragmentHashes[m.parts[0]] = sha(raw)
	if err := writeManifest(manifestPath, m, m.links); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("- Новое правило.\n- Другое правило.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stderr
	os.Stderr = w
	err = ImportWithOptions(codex, output, ImportOptions{Home: root, Refresh: true})
	os.Stderr = previous
	w.Close()
	warning, readErr := io.ReadAll(r)
	r.Close()
	if err != nil || readErr != nil || !strings.Contains(string(warning), "warning: "+m.parts[0]+":") || !strings.Contains(string(warning), "was not preserved") {
		t.Fatalf("refresh error = %v, warning = %q, read error = %v", err, warning, readErr)
	}
}
