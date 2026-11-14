package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAbsorbRoundTripEditBelowInsertedFragment(t *testing.T) {
	codex, out, manifestPath, m := purposeFixture(t, "- first\n\n- second\n\n- third\n")
	purposeSet(t, codex, true)
	purposeFragment(t, codex, manifestPath, m, 0, "first reason")
	m = mustReadManifest(t, manifestPath)
	purposeFragment(t, codex, manifestPath, m, 1, "second reason")
	m = mustReadManifest(t, manifestPath)
	purposeFragment(t, codex, manifestPath, m, 2, "third reason")
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	output = bytes.Replace(output, []byte("- second\n"), []byte("- inserted\n\n- second\n"), 1)
	output = bytes.Replace(output, []byte("- third\n"), []byte("- third edited\n"), 1)
	mustWrite(t, out, output)
	if err := Absorb(codex, out, AbsorbOptions{DraftDir: codex}); err != nil {
		t.Fatal(err)
	}
	updated, err := readManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFragmentBody(t, codex, updated.parts[len(updated.parts)-1]); string(got) != "- third edited\n" {
		t.Fatalf("last fragment body = %q", got)
	}
	assertAbsorbedOutputEquals(t, codex, out, output)
}

func TestAbsorbRoundTripAdjacentFragmentEdits(t *testing.T) {
	codex, out, manifestPath, m := purposeFixture(t, "- first\n- second\n")
	purposeSet(t, codex, true)
	purposeFragment(t, codex, manifestPath, m, 0, "first reason")
	m = mustReadManifest(t, manifestPath)
	purposeFragment(t, codex, manifestPath, m, 1, "second reason")
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	output = bytes.Replace(output, []byte("- first\n"), []byte("- first edited\n"), 1)
	output = bytes.Replace(output, []byte("- second\n"), []byte("- second edited\n"), 1)
	mustWrite(t, out, output)
	if err := Absorb(codex, out, AbsorbOptions{DraftDir: codex}); err != nil {
		t.Fatal(err)
	}
	updated, err := readManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFragmentBody(t, codex, updated.parts[0]); string(got) != "- first edited\n" {
		t.Fatalf("first fragment body = %q", got)
	}
	if got := readFragmentBody(t, codex, updated.parts[1]); string(got) != "- second edited\n" {
		t.Fatalf("second fragment body = %q", got)
	}
	assertAbsorbedOutputEquals(t, codex, out, output)
}

func TestAbsorbRoundTripLineAddedAbovePurpose(t *testing.T) {
	codex, out, manifestPath, m := purposeFixture(t, "Rule one\n")
	purposeSet(t, codex, true)
	purposeFragment(t, codex, manifestPath, m, 0, "rule reason")
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	output = bytes.Replace(output, []byte("Rule one\n"), []byte("Rule one\nAdditional line\n"), 1)
	mustWrite(t, out, output)
	if err := Absorb(codex, out, AbsorbOptions{DraftDir: codex}); err != nil {
		t.Fatal(err)
	}
	updated, err := readManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.parts) != 1 {
		t.Fatalf("manifest parts = %v", updated.parts)
	}
	if got := readFragmentBody(t, codex, updated.parts[0]); string(got) != "Rule one\nAdditional line\n" {
		t.Fatalf("fragment body = %q", got)
	}
	assertAbsorbedOutputEquals(t, codex, out, output)
}

func TestAbsorbRoundTripInsertBelowPurpose(t *testing.T) {
	var input strings.Builder
	input.WriteString("# Guide\n\nIntro line.\n\n## Tasks\n")
	for _, name := range []string{"one", "two", "three", "four", "five", "six", "seven"} {
		input.WriteString("- rule " + name + " text\n")
	}
	input.WriteString("\n## Work\n- rule eight text\n- rule nine text\n")
	codex, out, manifestPath, m := purposeFixture(t, input.String())
	purposeSet(t, codex, true)
	targetGoal := ""
	for i, id := range m.parts {
		m = mustReadManifest(t, manifestPath)
		body := readFragmentBody(t, codex, id)
		goal := "reason " + strconv.Itoa(i)
		if bytes.Contains(body, []byte("- rule seven text")) {
			targetGoal = goal
		}
		purposeFragment(t, codex, manifestPath, m, i, goal)
	}
	m = mustReadManifest(t, manifestPath)
	m.header = true
	if err := writeManifest(manifestPath, m, m.links); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	marker := []byte(purposeMarker + targetGoal + "\n")
	output = bytes.Replace(output, marker, append(append([]byte(nil), marker...), []byte("- rule tail text\n")...), 1)
	mustWrite(t, out, output)
	if err := Absorb(codex, out, AbsorbOptions{DraftDir: codex}); err != nil {
		t.Fatal(err)
	}
	assertAbsorbedOutputEquals(t, codex, out, output)
}

func TestAbsorbRoundTripTwoInsertionsAndEditBelow(t *testing.T) {
	codex, out, manifestPath, m := purposeFixture(t, "- first\n\n- second\n\n- third\n\n- fourth\n")
	purposeSet(t, codex, true)
	setPurposeGoals(t, codex, manifestPath, m, "first", "second", "third", "fourth")
	m = mustReadManifest(t, manifestPath)
	fourth := m.parts[3]
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	output = bytes.Replace(output, []byte("- second\n"), []byte("- inserted one\n\n- second\n"), 1)
	marker := []byte(purposeMarker + "third\n")
	output = bytes.Replace(output, marker, append(append([]byte(nil), marker...), []byte("- inserted two\n")...), 1)
	output = bytes.Replace(output, []byte("- fourth\n"), []byte("- fourth edited\n"), 1)
	mustWrite(t, out, output)
	if err := Absorb(codex, out, AbsorbOptions{DraftDir: codex}); err != nil {
		t.Fatal(err)
	}
	if got := readFragmentBody(t, codex, fourth); string(got) != "- fourth edited\n" {
		t.Fatalf("fourth fragment body = %q", got)
	}
	assertAbsorbedOutputEquals(t, codex, out, output)
}

func TestAbsorbRoundTripAddedLineNextToEditedNeighbour(t *testing.T) {
	codex, out, manifestPath, m := purposeFixture(t, "- first\n- second\n")
	purposeSet(t, codex, true)
	setPurposeGoals(t, codex, manifestPath, m, "first", "second")
	m = mustReadManifest(t, manifestPath)
	first, second := m.parts[0], m.parts[1]
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	output = bytes.Replace(output, []byte("- first\n"), []byte("- first\n  second line\n"), 1)
	output = bytes.Replace(output, []byte("- second\n"), []byte("- second edited\n"), 1)
	mustWrite(t, out, output)
	if err := Absorb(codex, out, AbsorbOptions{DraftDir: codex}); err != nil {
		t.Fatal(err)
	}
	if got := readFragmentBody(t, codex, first); string(got) != "- first\n  second line\n" {
		t.Fatalf("first fragment body = %q", got)
	}
	if got := readFragmentBody(t, codex, second); string(got) != "- second edited\n" {
		t.Fatalf("second fragment body = %q", got)
	}
	assertAbsorbedOutputEquals(t, codex, out, output)
}

func TestAbsorbRoundTripInsertAtTop(t *testing.T) {
	codex, out, _, _ := purposeFixture(t, "first\n\nsecond\n")
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte("new top\n\n"), original...)
	mustWrite(t, out, want)
	if err := Absorb(codex, out, AbsorbOptions{DraftDir: codex}); err != nil {
		t.Fatal(err)
	}
	manifest := mustReadManifest(t, filepath.Join(codex, "manifest", "out.md.yaml"))
	if len(manifest.parts) != 3 || string(readFragmentBody(t, codex, manifest.parts[0])) != "new top\n\n" {
		t.Fatalf("top insertion parts = %v", manifest.parts)
	}
	assertAbsorbedOutputEquals(t, codex, out, want)
}

func readFragmentBody(t *testing.T, codex, id string) []byte {
	t.Helper()
	path := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body, err := fragmentBody(raw)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func mustReadManifest(t *testing.T, path string) manifest {
	t.Helper()
	m, err := readManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func setPurposeGoals(t *testing.T, codex, manifestPath string, m manifest, goals ...string) {
	t.Helper()
	for i, goal := range goals {
		m = mustReadManifest(t, manifestPath)
		purposeFragment(t, codex, manifestPath, m, i, goal)
	}
}
