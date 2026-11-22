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

// subHeaderFixture is warningFixture with the lex header turned on, as lex sub expects of a built file.
func subHeaderFixture(t *testing.T) (string, string) {
	t.Helper()
	_, codex, output := warningFixture(t)
	manifests, err := filepath.Glob(filepath.Join(codex, "manifest", "*.yaml"))
	if err != nil || len(manifests) != 1 {
		t.Fatalf("manifests = %v, %v", manifests, err)
	}
	data, err := os.ReadFile(manifests[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifests[0], bytes.Replace(data, []byte("header: false"), []byte("header: true"), 1), 0644); err != nil {
		t.Fatal(err)
	}
	if err := lex.BuildWithOptions(codex, lex.BuildOptions{Force: true, Output: output}); err != nil {
		t.Fatal(err)
	}
	return codex, output
}

func TestSubCommandEditsFragment(t *testing.T) {
	codex, output := subHeaderFixture(t)
	stdout, err := captureRun(t, "sub", "--codex", codex, "Original rule.", "Edited rule.", output)
	if err != nil {
		t.Fatalf("sub: %v", err)
	}
	data, _ := os.ReadFile(output)
	if !strings.Contains(string(data), "Edited rule.") {
		t.Fatalf("output = %q", data)
	}
	if !strings.Contains(stdout, "/frag/") || !strings.Contains(stdout, "/manifest/") {
		t.Fatalf("stdout lacks changed codex files: %q", stdout)
	}
	if _, err := captureRun(t, "build", "--codex", codex, "--check"); err != nil {
		t.Fatalf("build --check: %v", err)
	}
	// Markdown list text may start with "-": it is OLD or NEW, not a flag, as in the sub tool. Turning the paragraph
	// into an item changes the fragment structure, so it is refused (exit 1), not a usage error (exit 2).
	_, err = captureRun(t, "sub", "--codex", codex, "Edited rule.", "- Edited rule.", output)
	if err == nil || exitCode(err) != 1 || !strings.Contains(err.Error(), "structure of fragment") {
		t.Fatalf("sub with NEW starting with -: %v", err)
	}
	_, err = captureRun(t, "sub", "--codex", codex, "- Edited rule.", "Edited rule.", output)
	if err == nil || exitCode(err) != 1 || !strings.Contains(err.Error(), "found 0") {
		t.Fatalf("sub with OLD starting with -: %v", err)
	}
}

func TestSubCommandRefusalNamesFallback(t *testing.T) {
	codex, output := subHeaderFixture(t)
	before, _ := os.ReadFile(output)
	_, err := captureRun(t, "sub", "--codex", codex, "собран из", "взят из", output)
	if err == nil {
		t.Fatal("header edit accepted")
	}
	if exitCode(err) != 1 {
		t.Fatalf("exit code = %d", exitCode(err))
	}
	lines := strings.Split(err.Error(), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "lex header") {
		t.Fatalf("refusal = %q", err.Error())
	}
	if !strings.HasPrefix(lines[1], "fallback: ") || !strings.Contains(lines[1], "sub -- ") || !strings.Contains(lines[1], "&& lex absorb --codex "+codex+" "+output) {
		t.Fatalf("fallback line = %q", lines[1])
	}
	after, _ := os.ReadFile(output)
	if !bytes.Equal(before, after) {
		t.Fatal("refused sub changed the file")
	}
	_, err = captureRun(t, "sub", "--codex", codex, "нет такого текста", "x", output)
	if err == nil || exitCode(err) != 1 || !strings.Contains(err.Error(), "found 0") || !strings.Contains(err.Error(), "fallback: ") {
		t.Fatalf("count refusal = %v", err)
	}
	for _, args := range [][]string{
		{"sub", "--codex", codex, "a", "b"},
		{"sub", "--codex", codex, "-F", "spec", "-n", "2", output},
		{"sub", "a", "b", output},
		{"sub", "--codex", codex, "--bogus", "a", "b", output},
	} {
		if _, err := captureRun(t, args...); err == nil || exitCode(err) != 2 {
			t.Errorf("%q: err = %v, want usage error", args, err)
		}
	}
}

func TestSubCommandSpecFile(t *testing.T) {
	codex, output := subHeaderFixture(t)
	spec := t.TempDir() + "/spec"
	if err := os.WriteFile(spec, []byte("<<<<<<< SEARCH\nOriginal rule.\n=======\nSpec rule.\n>>>>>>> REPLACE\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sub", "--codex", codex, "-F", spec, output); err != nil {
		t.Fatalf("sub -F: %v", err)
	}
	data, _ := os.ReadFile(output)
	if !strings.Contains(string(data), "Spec rule.") {
		t.Fatalf("output = %q", data)
	}
}

// subSharedFixture builds two outputs from one shared fragment in a codex under HOME/.claude, with a config repository
// beside it, so lex prints the git add line.
func subSharedFixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	codex := filepath.Join(home, ".claude", "codex")
	if err := os.MkdirAll(filepath.Join(home, ".claude-config.git"), 0755); err != nil {
		t.Fatal(err)
	}
	frags := map[string]string{"a/own": "# A\n", "b/own": "# B\n", "common/shared": "- общий пункт\n"}
	for id, body := range frags {
		path := filepath.Join(codex, "frag", id+".md")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		raw := "---\nid: " + id + "\nцель: \nоснование: тест\nснять_когда: тест\n---\n" + body
		if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
			t.Fatal(err)
		}
	}
	outA, outB := filepath.Join(home, "a.md"), filepath.Join(home, "b.md")
	if err := os.MkdirAll(filepath.Join(codex, "manifest"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ key, out, own string }{{"a.md", outA, "a/own"}, {"b.md", outB, "b/own"}} {
		manifest := "out: \"" + item.out + "\"\nsource_sha256: fixture\nbuilt_sha256: \nheader: true\nparts:\n  - \"" + item.own + "\"\n  - \"common/shared\"\nfragment_sha256:\n"
		if err := os.WriteFile(filepath.Join(codex, "manifest", item.key+".yaml"), []byte(manifest), 0644); err != nil {
			t.Fatal(err)
		}
		if err := lex.BuildWithOptions(codex, lex.BuildOptions{Force: true, Output: item.out}); err != nil {
			t.Fatal(err)
		}
	}
	return home, codex, outA, outB
}

func TestSubCommandSharedFragmentAndGitAdd(t *testing.T) {
	home, codex, outA, outB := subSharedFixture(t)
	stdout, err := captureRun(t, "sub", "--codex", codex, "общий пункт", "общий пункт ЖЖ", outA)
	if err != nil {
		t.Fatalf("sub: %v", err)
	}
	if !strings.Contains(stdout, "shared fragment: also rebuilt "+outB+"\n") {
		t.Fatalf("stdout lacks the other output: %q", stdout)
	}
	other, _ := os.ReadFile(outB)
	if !strings.Contains(string(other), "общий пункт ЖЖ") {
		t.Fatalf("other output = %q", other)
	}
	gitAdd := "git --git-dir=" + filepath.Join(home, ".claude-config.git") + " --work-tree=" + home + " add -- "
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, gitAdd) || !strings.Contains(last, filepath.Join(codex, "frag", "common", "shared.md")) ||
		!strings.Contains(last, filepath.Join(codex, "manifest", "a.md.yaml")) || !strings.Contains(last, filepath.Join(codex, "manifest", "b.md.yaml")) {
		t.Fatalf("git add line = %q", last)
	}
}

func TestSubCommandNoChange(t *testing.T) {
	codex, output := subHeaderFixture(t)
	before, _ := os.ReadFile(output)
	stdout, err := captureRun(t, "sub", "--codex", codex, "Original rule.", "Original rule.", output)
	if err != nil {
		t.Fatalf("sub: %v", err)
	}
	if !strings.Contains(stdout, "nothing changed") {
		t.Fatalf("stdout = %q", stdout)
	}
	after, _ := os.ReadFile(output)
	if !bytes.Equal(before, after) {
		t.Fatal("no-op sub changed the file")
	}
}

// The fallback line is a working one-line bash command even when OLD or NEW holds a line break.
func TestSubFallbackQuotesLineBreaks(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	for _, arg := range []string{"two\nlines", "tab\tand 'quote' and \\ back", "cr\r\n", "plain - text"} {
		quoted := subShellArg(arg)
		if strings.ContainsAny(quoted, "\n\r") {
			t.Fatalf("%q quoted over lines: %q", arg, quoted)
		}
		out, err := exec.Command(bash, "-c", "printf %s "+quoted).Output()
		if err != nil || string(out) != arg {
			t.Fatalf("bash read %q back as %q (%v)", quoted, out, err)
		}
	}
	codex, output := subHeaderFixture(t)
	_, err = captureRun(t, "sub", "--codex", codex, "no such\ntext", "x\n", output)
	if err == nil || !strings.Contains(err.Error(), "sub -- $'no such\\ntext' $'x\\n' ") {
		t.Fatalf("fallback = %v", err)
	}
}
