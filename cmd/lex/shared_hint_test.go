package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

func TestForceHintSharedOnlyWhenSharedPartDiffers(t *testing.T) {
	codex, _, outB, _ := forceHintFixture(t)
	manifestB, err := os.ReadFile(filepath.Join(codex, "manifest", "beta.md.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	parts, err := manifestParts(manifestB)
	if err != nil {
		t.Fatal(err)
	}
	manifestA, err := os.ReadFile(filepath.Join(codex, "manifest", "alpha.md.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	sharedParts, err := manifestParts(manifestA)
	if err != nil {
		t.Fatal(err)
	}
	shared := make(map[string]bool)
	for _, part := range sharedParts {
		shared[part] = true
	}
	var ownPart string
	for _, part := range parts {
		if !shared[part] {
			ownPart = part
			break
		}
	}
	if ownPart == "" {
		t.Fatalf("B has no private fragment: %v", parts)
	}
	ownFragment := filepath.Join(codex, "frag", filepath.FromSlash(ownPart)+".md")
	if err := editFileTextForHintTest(ownFragment, "more\n", "context\nmore\nfragment edit\n"); err != nil {
		t.Fatal(err)
	}
	if err := editFileTextForHintTest(outB, "more\n", "context\nmore\nfile edit\n"); err != nil {
		t.Fatal(err)
	}
	_, err = captureRun(t, "absorb", "--codex", codex, outB)
	if err == nil || strings.Contains(err.Error(), "--shared") {
		t.Fatalf("refusal for a private edit = %v", err)
	}
	if _, err := captureRun(t, "absorb", "--force", "--codex", codex, outB); err != nil {
		t.Fatalf("absorb --force without --shared: %v", err)
	}
}

func TestForceSharedNamesOutputsToRebuild(t *testing.T) {
	codex, outA, outB, _ := forceHintFixture(t)
	if err := editFileTextForHintTest(outA, "alpha", "ALPHA"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "absorb", "--codex", codex, "--shared", outA); err != nil {
		t.Fatalf("absorb A shared edit: %v", err)
	}
	if err := editFileTextForHintTest(outB, "alpha", "BETA"); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := captureRunWithStderr(t, "absorb", "--force", "--shared", "--codex", codex, outB)
	if err != nil {
		t.Fatalf("absorb B: %v", err)
	}
	want := "warning: " + outA + ": its shared part no longer matches the fragments this absorb wrote; `lex build --codex " + strconv.Quote(codex) + " " + strconv.Quote(outA) + "` replaces that part with the absorbed text"
	if !strings.Contains(stderr, want) {
		t.Fatalf("stderr %q does not contain warning %q", stderr, want)
	}
	for _, r := range stderr {
		if unicode.Is(unicode.Cyrillic, r) {
			t.Fatalf("warning contains Cyrillic text: %q", stderr)
		}
	}
	if !strings.Contains(stderr, outA) || !strings.Contains(stderr, "lex build") {
		t.Fatalf("warning does not name the other output and rebuild command: %q", stderr)
	}
	contents, err := os.ReadFile(outA)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(contents, []byte("ALPHA")) || bytes.Contains(contents, []byte("BETA")) {
		t.Fatalf("absorb changed A: %q", contents)
	}
}

func manifestParts(data []byte) ([]string, error) {
	var parts []string
	inside := false
	for line := range strings.SplitSeq(string(data), "\n") {
		if line == "parts:" {
			inside = true
			continue
		}
		if inside && strings.HasPrefix(line, "  - ") {
			part, err := strconv.Unquote(strings.TrimPrefix(line, "  - "))
			if err != nil {
				return nil, fmt.Errorf("unquote manifest part %q: %w", line, err)
			}
			parts = append(parts, part)
			continue
		}
		if inside && line != "" && !strings.HasPrefix(line, "  ") {
			break
		}
	}
	return parts, nil
}

func editFileTextForHintTest(path, old, replacement string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if !bytes.Contains(data, []byte(old)) {
		return os.ErrNotExist
	}
	return os.WriteFile(path, bytes.Replace(data, []byte(old), []byte(replacement), 1), 0644)
}
