package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func noHashFixture(t *testing.T) (string, string, string, manifest, string, string) {
	t.Helper()
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	output := filepath.Join(root, "doc.md")
	mustWrite(t, output, []byte("first paragraph\n\nsecond paragraph\n"))
	if err := ImportWithOptions(codex, output, ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	manifestPath := manifestOf(t, codex, "doc.md")
	m, err := readManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.parts) != 2 {
		t.Fatalf("fixture has %d parts, want 2: %#v", len(m.parts), m.parts)
	}
	return root, codex, output, m, manifestPath, filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md")
}

func makePartsUnhashed(t *testing.T, manifestPath string, m manifest, ids ...string) {
	t.Helper()
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		line := "  " + strconv.Quote(id) + ": " + m.fragmentHashes[id] + "\n"
		if !bytes.Contains(raw, []byte(line)) {
			t.Fatalf("manifest has no hash line %q", line)
		}
		raw = bytes.Replace(raw, []byte(line), nil, 1)
	}
	mustWrite(t, manifestPath, raw)
}

func assertNoHashLine(t *testing.T, manifestPath, id string) {
	t.Helper()
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("  "+strconv.Quote(id)+":")) {
		t.Fatalf("manifest still has a hash entry for unhashed part %q:\n%s", id, raw)
	}
}

func assertManifestHealthy(t *testing.T, codex string) {
	t.Helper()
	if err := Build(codex, true); err != nil {
		t.Fatalf("build --check: %v", err)
	}
	if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
		t.Fatalf("check: %v", err)
	}
}

func TestManifestNoHashAbsorbOther(t *testing.T) {
	_, codex, output, m, manifestPath, _ := noHashFixture(t)
	makePartsUnhashed(t, manifestPath, m, m.parts[0])
	updated := []byte("first paragraph\n\nsecond paragraph edited\n")
	mustWrite(t, output, updated)
	if err := Absorb(codex, output, AbsorbOptions{}); err != nil {
		t.Fatal(err)
	}
	assertNoHashLine(t, manifestPath, m.parts[0])
	assertManifestHealthy(t, codex)
}

func TestManifestNoHashQuotes(t *testing.T) {
	_, codex, output, m, manifestPath, _ := noHashFixture(t)
	makePartsUnhashed(t, manifestPath, m, m.parts[0])
	mustWrite(t, output, []byte("first paragraph\n\nsecond paragraph (пользователь 03.02: «цитата»)\n"))
	if err := Absorb(codex, output, AbsorbOptions{}); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(t.TempDir(), "quotes.json")
	if err := QuotesToBasis(codex, QuotesToBasisOptions{Apply: true, Report: report}); err != nil {
		t.Fatal(err)
	}
	assertNoHashLine(t, manifestPath, m.parts[0])
	assertManifestHealthy(t, codex)
}

func TestManifestNoHashImportSymlink(t *testing.T) {
	root, codex, output, m, manifestPath, _ := noHashFixture(t)
	makePartsUnhashed(t, manifestPath, m, m.parts[0])
	link := filepath.Join(root, "link.md")
	if err := os.Symlink(filepath.Base(output), link); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, link, ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	assertNoHashLine(t, manifestPath, m.parts[0])
	assertManifestHealthy(t, codex)
}

func TestManifestNoHashAbsorbSelf(t *testing.T) {
	_, codex, output, m, manifestPath, unhashedFragment := noHashFixture(t)
	makePartsUnhashed(t, manifestPath, m, m.parts[0], m.parts[1])
	mustWrite(t, output, []byte("first paragraph edited\n\nsecond paragraph\n"))
	if err := Absorb(codex, output, AbsorbOptions{}); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(unhashedFragment)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := readManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := parsed.fragmentHashes[m.parts[0]], sha(updated); got != want {
		t.Fatalf("edited part hash = %q, want file hash %q", got, want)
	}
	assertNoHashLine(t, manifestPath, m.parts[1])
	if err := Build(codex, true); err != nil {
		t.Fatalf("build --check: %v", err)
	}
}

func TestManifestNoHashRefreshRefuses(t *testing.T) {
	root, codex, output, m, manifestPath, firstFragment := noHashFixture(t)
	makePartsUnhashed(t, manifestPath, m, m.parts[0])
	beforeManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	fragmentsBefore := map[string][]byte{}
	for _, id := range m.parts {
		path := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
		fragmentsBefore[path], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	newSource := []byte("changed paragraph\n\nsecond paragraph\n")
	mustWrite(t, output, newSource)
	err = ImportWithOptions(codex, output, ImportOptions{Refresh: true, Home: root})
	if err == nil || !strings.Contains(err.Error(), "fragment body was edited") {
		t.Fatalf("refresh error = %v, want edited fragment refusal", err)
	}
	afterManifest, err := os.ReadFile(manifestPath)
	if err != nil || !bytes.Equal(beforeManifest, afterManifest) {
		t.Fatalf("refresh changed manifest: %v", err)
	}
	for path, before := range fragmentsBefore {
		after, readErr := os.ReadFile(path)
		if readErr != nil || !bytes.Equal(before, after) {
			t.Fatalf("refresh changed fragment %s: %v", path, readErr)
		}
	}
	if _, err := os.Stat(firstFragment); err != nil {
		t.Fatalf("first fragment missing: %v", err)
	}
}

func TestManifestEmptyHashValue(t *testing.T) {
	_, codex, _, m, manifestPath, _ := noHashFixture(t)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	valid := "  " + strconv.Quote(m.parts[0]) + ": " + m.fragmentHashes[m.parts[0]] + "\n"
	if !bytes.Contains(raw, []byte(valid)) {
		t.Fatalf("manifest has no hash line %q", valid)
	}
	raw = bytes.Replace(raw, []byte(valid), []byte("  "+strconv.Quote(m.parts[0])+": \n"), 1)
	mustWrite(t, manifestPath, raw)
	parsed, err := readManifest(manifestPath)
	if err != nil {
		t.Fatalf("empty hash should be treated as absent: %v", err)
	}
	if _, ok := parsed.fragmentHashes[m.parts[0]]; ok {
		t.Fatalf("empty hash value was retained: %#v", parsed.fragmentHashes)
	}
	assertManifestHealthy(t, codex)
}
