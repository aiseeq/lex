package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiseeq/lex/internal/lex"
)

func TestHintCommandSurvivesShell(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	codex := filepath.Join(root, "codex")
	out := filepath.Join(root, "folder $HOME `tick`", "source.md")
	if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, []byte("before\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := lex.ImportWithOptions(codex, out, lex.ImportOptions{Home: root}); err != nil {
		t.Fatalf("import: %v", err)
	}
	if err := os.WriteFile(out, []byte("after\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := captureRun(t, "check", "--codex", codex)
	if err == nil {
		t.Fatal("check unexpectedly succeeded")
	}
	message := err.Error()
	start := strings.Index(message, "`lex absorb --codex ")
	if start < 0 {
		t.Fatalf("check error has no absorb hint: %v", err)
	}
	start++
	end := strings.LastIndex(message, "`")
	if end <= start {
		t.Fatalf("unterminated command hint: %q", message[start:])
	}
	commandText := message[start:end]

	binDir := t.TempDir()
	shim := filepath.Join(binDir, "lex")
	shimText := "#!/bin/sh\ntest \"$1\" = absorb && test \"$2\" = --codex && test \"$3\" = \"$EXPECTED_CODEX\" && test \"$4\" = \"$EXPECTED_OUT\"\n"
	if err := os.WriteFile(shim, []byte(shimText), 0755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", commandText)
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"), "EXPECTED_CODEX="+codex, "EXPECTED_OUT="+out)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hint command %q failed: %v; output: %s", commandText, err, bytes.TrimSpace(output))
	}
}

func TestDocsDescribeSharedHint(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "shared-fragment hint includes `--shared` and names the other files") {
		t.Error("README lex absorb description no longer says the shared hint includes --shared and names the other files")
	}
	if !strings.Contains(guideText, "Подсказка для общего фрагмента уже содержит --shared и называет другие файлы") {
		t.Error("lex guide no longer says the shared hint includes --shared and names the other files")
	}
}
