package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aiseeq/lex/internal/lex"
)

func forceHintFixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	codex := filepath.Join(root, "codex")
	outA, outB := filepath.Join(root, "alpha.md"), filepath.Join(root, "beta.md")
	if err := os.WriteFile(outA, []byte("alpha\n\nbeta\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outB, []byte("other\n\nmore\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{outA, outB} {
		if err := lex.ImportWithOptions(codex, output, lex.ImportOptions{Home: root}); err != nil {
			t.Fatalf("import %s: %v", output, err)
		}
	}
	manifestA := filepath.Join(codex, "manifest", "alpha.md.yaml")
	manifestB := filepath.Join(codex, "manifest", "beta.md.yaml")
	a, err := os.ReadFile(manifestA)
	if err != nil {
		t.Fatalf("read manifest A: %v", err)
	}
	b, err := os.ReadFile(manifestB)
	if err != nil {
		t.Fatalf("read manifest B: %v", err)
	}
	partA, err := firstManifestPart(a)
	if err != nil {
		t.Fatalf("part A: %v", err)
	}
	partB, err := firstManifestPart(b)
	if err != nil {
		t.Fatalf("part B: %v", err)
	}
	old := strconv.Quote(partB)
	if !bytes.Contains(b, []byte(old)) {
		t.Fatalf("manifest B lacks part %q", partB)
	}
	b = []byte(strings.ReplaceAll(string(b), old, strconv.Quote(partA)))
	if err := os.WriteFile(manifestB, b, 0644); err != nil {
		t.Fatalf("share part in manifest B: %v", err)
	}
	if _, err := captureRun(t, "build", "--codex", codex, "--force", outB); err != nil {
		t.Fatalf("build shared output B: %v", err)
	}
	return codex, outA, outB, filepath.Join(root, "")
}

func firstManifestPart(data []byte) (string, error) {
	inside := false
	for line := range strings.SplitSeq(string(data), "\n") {
		if line == "parts:" {
			inside = true
			continue
		}
		if inside && strings.HasPrefix(line, "  - ") {
			return strconv.Unquote(strings.TrimPrefix(line, "  - "))
		}
		if inside && line != "" && !strings.HasPrefix(line, "  ") {
			break
		}
	}
	return "", strconv.ErrSyntax
}

func commandFromHint(hint string) ([]string, error) {
	var args []string
	for len(hint) > 0 {
		hint = strings.TrimLeft(hint, " \t")
		if hint == "" {
			break
		}
		if hint[0] == '"' {
			end := 1
			for end < len(hint) {
				if hint[end] == '"' && hint[end-1] != '\\' {
					break
				}
				end++
			}
			if end == len(hint) {
				return nil, strconv.ErrSyntax
			}
			value, err := strconv.Unquote(hint[:end+1])
			if err != nil {
				return nil, err
			}
			args = append(args, value)
			hint = hint[end+1:]
			continue
		}
		end := strings.IndexAny(hint, " \t")
		if end < 0 {
			args = append(args, hint)
			break
		}
		args = append(args, hint[:end])
		hint = hint[end:]
	}
	if len(args) == 0 || args[0] != "lex" {
		return nil, strconv.ErrSyntax
	}
	return args[1:], nil
}

// sharedForceScenario follows the hint of source ("absorb", "status" or "check") for a file whose --force would change a shared fragment.
func sharedForceScenario(t *testing.T, source string) {
	t.Helper()
	codex, outA, outB, _ := forceHintFixture(t)
	a, err := os.ReadFile(outA)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outA, bytes.Replace(a, []byte("alpha"), []byte("ALPHA"), 1), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "absorb", "--codex", codex, "--shared", outA); err != nil {
		t.Fatalf("absorb A shared edit: %v", err)
	}
	b, err := os.ReadFile(outB)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outB, bytes.Replace(b, []byte("more"), []byte("MORE"), 1), 0644); err != nil {
		t.Fatal(err)
	}
	var commandText string
	if source == "status" {
		stdout, err := captureRun(t, "status", "--codex", codex)
		if err != nil && exitCode(err) != 1 {
			t.Fatalf("status: %v; output %q", err, stdout)
		}
		for line := range strings.SplitSeq(stdout, "\n") {
			if strings.Contains(line, outB) {
				start := strings.Index(line, "run ")
				end := strings.Index(line, " to keep the file's text")
				if start >= 0 && end > start {
					commandText = line[start+4 : end]
				}
				if !strings.Contains(line[end:], outA) {
					t.Fatalf("status line does not name shared output A %q: %q", outA, line)
				}
			}
		}
	} else {
		var err error
		if source == "check" {
			_, err = captureRun(t, "check", "--codex", codex)
		} else {
			_, err = captureRun(t, "absorb", "--codex", codex, outB)
		}
		if err == nil {
			t.Fatalf("%s of B unexpectedly succeeded", source)
		}
		text := err.Error()
		start := strings.Index(text, "`lex absorb --force")
		if start >= 0 {
			start++
			end := strings.Index(text[start:], "`")
			if end >= 0 {
				commandText = text[start : start+end]
			}
		}
		if !strings.Contains(text, outA) {
			t.Fatalf("%s does not name shared output A %q: %v", source, outA, err)
		}
	}
	args, err := commandFromHint(commandText)
	if err != nil {
		t.Fatalf("parse suggested command %q: %v", commandText, err)
	}
	if !strings.Contains(strings.Join(args, " "), "--shared") {
		t.Fatalf("suggested command lacks --shared: %q", commandText)
	}
	if _, err := captureRun(t, args...); err != nil {
		t.Fatalf("suggested command %q failed: %v", commandText, err)
	}
}

func TestAbsorbForceSharedHintRuns(t *testing.T) { sharedForceScenario(t, "absorb") }
func TestStatusForceSharedHintRuns(t *testing.T) { sharedForceScenario(t, "status") }
func TestCheckForceSharedHintRuns(t *testing.T)  { sharedForceScenario(t, "check") }

// noBuiltHashFixture builds one output and drops built_sha256 from its manifest; it returns the codex, the output and its first fragment.
func noBuiltHashFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	out := filepath.Join(root, "source.md")
	if err := os.WriteFile(out, []byte("seed\n\nstore\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := lex.ImportWithOptions(codex, out, lex.ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "build", "--codex", codex); err != nil {
		t.Fatalf("build: %v", err)
	}
	manifests, err := filepath.Glob(filepath.Join(codex, "manifest", "*.yaml"))
	if err != nil || len(manifests) != 1 {
		t.Fatalf("manifests %v: %v", manifests, err)
	}
	raw, err := os.ReadFile(manifests[0])
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for line := range strings.SplitSeq(string(raw), "\n") {
		if !strings.HasPrefix(line, "built_sha256: ") {
			lines = append(lines, line)
		}
	}
	if err := os.WriteFile(manifests[0], []byte(strings.Join(lines, "\n")), 0644); err != nil {
		t.Fatal(err)
	}
	fragment, err := firstFragment(codex)
	if err != nil {
		t.Fatal(err)
	}
	return codex, out, fragment
}

func TestAbsorbRefusalWithoutBuiltHashNamesSource(t *testing.T) {
	codex, out, fragment := noBuiltHashFixture(t)
	editFileText(t, fragment, "---\nseed\n", "---\nSEED\n")
	editFileText(t, out, "store", "STORE")
	_, err := captureRun(t, "absorb", "--codex", codex, out)
	if err == nil || !strings.Contains(err.Error(), "differs from its recorded source") || strings.Contains(err.Error(), "since the last build") {
		t.Fatalf("refusal without built hash = %v", err)
	}
}

// Without built_sha256 and with only the fragments changed, the refusal names the manifest, not a last build.
func TestAbsorbRefusalWithoutBuiltHashFragmentsOnly(t *testing.T) {
	codex, out, fragment := noBuiltHashFixture(t)
	editFileText(t, fragment, "---\nseed\n", "---\nSEED\n")
	_, err := captureRun(t, "absorb", "--codex", codex, out)
	if err == nil || !strings.Contains(err.Error(), ": the fragments differ from the manifest;") || strings.Contains(err.Error(), "since the last build") {
		t.Fatalf("refusal without built hash, fragments only = %v", err)
	}
}

func TestAbsorbAcceptsOldHeaderForm(t *testing.T) {
	codex, output, built := headerFixture(t)
	fragment, err := fragmentContaining(t, codex, "beds")
	if err != nil {
		t.Fatal(err)
	}
	editFragmentBodyText(t, fragment, "beds", "plants")
	updated := bytes.Replace(built, []byte("beds"), []byte("plants"), 1)
	root := os.Getenv("HOME")
	updated = bytes.ReplaceAll(updated, []byte("~/"), []byte(filepath.ToSlash(root)+"/"))
	if !bytes.Contains(updated, []byte(filepath.ToSlash(root))) {
		t.Fatalf("fixture header lacks home-relative path: %q", built)
	}
	if err := os.WriteFile(output, updated, 0644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "absorb", "--codex", codex, output); err != nil {
		t.Fatalf("absorb old header form: %v", err)
	}
	after, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("absorb changed old header output: got %q want %q", after, before)
	}
}

func TestCheckHintQuotesPaths(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex space")
	out := filepath.Join(root, "garden file.md")
	if err := os.WriteFile(out, []byte("plant\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := lex.ImportWithOptions(codex, out, lex.ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	editFileText(t, out, "plant", "plants")
	_, err := captureRun(t, "check", "--codex", codex)
	if err == nil {
		t.Fatal("check unexpectedly succeeded")
	}
	want := "lex absorb --codex " + strconv.Quote(codex) + " " + strconv.Quote(out)
	if !strings.Contains(err.Error(), want) {
		t.Errorf("check hint %q lacks %q", err, want)
	}
}

func TestReadmeNamesAbsorbForce(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	var synopsis string
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.Contains(line, "`lex absorb --codex ") {
			synopsis = line
			break
		}
	}
	if synopsis == "" {
		t.Fatal("README has no lex absorb synopsis")
	}
	for _, flag := range []string{"[--force]", "[--shared]"} {
		if !strings.Contains(synopsis, flag) {
			t.Errorf("README lex absorb synopsis lacks %s: %s", flag, synopsis)
		}
	}
}

func firstFragment(codex string) (string, error) {
	paths, err := filepath.Glob(filepath.Join(codex, "frag", "*", "*.md"))
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", os.ErrNotExist
	}
	return paths[0], nil
}

func editFileText(t *testing.T, path, old, replacement string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := bytes.Replace(data, []byte(old), []byte(replacement), 1)
	if bytes.Equal(data, updated) {
		t.Fatalf("%s does not contain %q", path, old)
	}
	if err := os.WriteFile(path, updated, 0644); err != nil {
		t.Fatal(err)
	}
}

func fragmentContaining(t *testing.T, codex, text string) (string, error) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(codex, "frag", "*", "*.md"))
	if err != nil {
		return "", err
	}
	for _, path := range paths {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read fragment %s: %v", path, readErr)
		}
		if strings.Contains(string(data), text) {
			return path, nil
		}
	}
	return "", os.ErrNotExist
}

func editFragmentBodyText(t *testing.T, path, old, replacement string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	separator := []byte("\n---\n")
	index := bytes.Index(data, separator)
	if index < 0 {
		t.Fatalf("fragment %s has no body separator", path)
	}
	bodyStart := index + len(separator)
	updatedBody := bytes.Replace(data[bodyStart:], []byte(old), []byte(replacement), 1)
	if bytes.Equal(updatedBody, data[bodyStart:]) {
		t.Fatalf("fragment %s body does not contain %q", path, old)
	}
	updated := append(append([]byte(nil), data[:bodyStart]...), updatedBody...)
	if err := os.WriteFile(path, updated, 0644); err != nil {
		t.Fatal(err)
	}
}
