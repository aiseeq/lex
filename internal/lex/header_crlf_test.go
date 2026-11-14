package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAbsorbCRLFHeaderKeepsOneHeader(t *testing.T) {
	for _, edit := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "edited"}[edit], func(t *testing.T) {
			dir := t.TempDir()
			out, codex := filepath.Join(dir, "a.md"), filepath.Join(dir, "cx")
			original := []byte("# A\n\n- one\n\n- two\n")
			mustWrite(t, out, original)
			if err := importData(codex, out, "a.md", original, false); err != nil {
				t.Fatal(err)
			}
			manifest := manifestOf(t, codex, "a.md")
			raw, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatal(err)
			}
			mustWrite(t, manifest, bytes.Replace(raw, []byte("header: false\n"), []byte("header: true\n"), 1))
			if err := BuildWithOptions(codex, BuildOptions{Force: true}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if edit {
				data = bytes.Replace(data, []byte("- two\n"), []byte("- two edited\n"), 1)
			}
			data = bytes.Replace(data, []byte("-->\n"), []byte("-->\r\n"), 1)
			mustWrite(t, out, data)
			if err := Absorb(codex, out, AbsorbOptions{DraftDir: codex}); err != nil {
				t.Fatalf("absorb: %v", err)
			}
			if err := BuildWithOptions(codex, BuildOptions{Force: true}); err != nil {
				t.Fatal(err)
			}
			built, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Count(built, []byte("<!-- lex: собран из ")) != 1 {
				t.Fatalf("header count = %d", bytes.Count(built, []byte("<!-- lex: собран из ")))
			}
			want := []byte("# A\n\n- one\n\n- two\n")
			if edit {
				want = []byte("# A\n\n- one\n\n- two edited\n")
			}
			if body := withoutGeneratedHeader(built, manifest); !bytes.Equal(body, want) {
				t.Fatalf("body = %q, want %q", body, want)
			}
		})
	}
}

func TestAdviceTextAboveHeaderNamesHeader(t *testing.T) {
	dir := t.TempDir()
	out, codex := filepath.Join(dir, "a.md"), filepath.Join(dir, "cx")
	original := []byte("# A\n\n- one\n")
	mustWrite(t, out, original)
	if err := importData(codex, out, "a.md", original, false); err != nil {
		t.Fatal(err)
	}
	manifest := manifestOf(t, codex, "a.md")
	raw, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, manifest, bytes.Replace(raw, []byte("header: false\n"), []byte("header: true\n"), 1))
	if err := BuildWithOptions(codex, BuildOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, out, append([]byte("intro\n"), data...))
	want := "text above lex header; move the text below the header"
	check, err := CheckWithWarnings(codex, CheckOptions{})
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("check advice = %q, error = %v", check, err)
	}
	status, err := Status(codex)
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != 1 || !strings.Contains(status[0].Problem, want) {
		t.Fatalf("status = %#v", status)
	}
}
