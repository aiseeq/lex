package lex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAbsorbPurposeLeftovers(t *testing.T) {
	r4 := "- rule one\n- rule two\n- rule three\n- rule four\n"
	g4 := []string{"g1", "g2", "g3", "g4"}
	ml := "- rule one\n  more one\n\n- rule two\n  more two\n\n- rule three\n  more three\n\n- rule four\n  more four\n"
	gml := []string{"g1", "g2", "g3", "g4"}
	sec := "# Title\n\n## A\n- rule one\n- rule two\n\n## B\n- rule three\n- rule four\n"
	gsec := []string{"", "", "g1", "g2", "", "g3", "g4"}
	rep := func(old, new string) func([]byte) []byte {
		return func(d []byte) []byte { return bytes.Replace(d, []byte(old), []byte(new), 1) }
	}
	chain := func(fs ...func([]byte) []byte) func([]byte) []byte {
		return func(d []byte) []byte {
			for _, f := range fs {
				d = f(d)
			}
			return d
		}
	}
	trimNL := func(d []byte) []byte { return bytes.TrimSuffix(d, []byte("\n")) }
	cases := []struct {
		name, input string
		goals       []string
		edit        func([]byte) []byte
	}{
		{"w-del3-edit4", r4, g4, chain(rep("- rule three\n", ""), rep("- rule four\n", "- rule four edited\n"))},
		{"w-del3-insert-blank-and-text", r4, g4, rep("- rule three\n  Зачем: g3\n", "\n  Зачем: g3\n\n- extra\n")},
		{"ml-del2-both-edit3", ml, gml, chain(rep("- rule two\n  more two\n", ""), rep("- rule three\n", "- rule three X\n"))},
		{"sec-del2-text-edit-headerB", sec, gsec, chain(rep("- rule two\n", ""), rep("## B\n", "## B edited\n"))},
		{"dup-delete-second-copy-text", "- same\n- other\n- same\n", []string{"g1", "g2", "g3"}, func(d []byte) []byte {
			i := bytes.LastIndex(d, []byte("- same\n"))
			return append(append([]byte(nil), d[:i]...), d[i+len("- same\n"):]...)
		}},
		{"dup-delete-middle-copy-text", "- other\n- same\n- same\n- tail\n", []string{"g1", "g2", "g3", "g4"}, func(d []byte) []byte {
			i := bytes.LastIndex(d, []byte("- same\n"))
			return append(append([]byte(nil), d[:i]...), d[i+len("- same\n"):]...)
		}},
		{"delete-text-rule1", r4, g4, rep("- rule one\n", "")},
		{"nonl-and-delete-rule4-whole", r4, g4, chain(rep("- rule four\n  Зачем: g4\n", ""), trimNL)},
		{"crlf-goal1-moved-under-rule3", r4, g4, chain(rep("  Зачем: g1\n", ""), rep("  Зачем: g3\n", "  Зачем: g3\n  Зачем: g1\r\n"))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			codex, out, _, _ := goalDeleteFixture(t, c.input, c.goals)
			msg := refusalAdvice(t, codex, out, c.edit)
			if c.name == "nonl-and-delete-rule4-whole" {
				if !strings.Contains(msg, "final newline") || strings.Contains(msg, "has no rule text") || strings.Contains(msg, "CRLF") {
					t.Fatalf("refusal = %q", msg)
				}
				return
			}
			if c.name == "crlf-goal1-moved-under-rule3" {
				if !(strings.Contains(msg, "CRLF") || strings.Contains(msg, "line endings")) || !strings.Contains(msg, "moved to another fragment") {
					t.Fatalf("refusal = %q", msg)
				}
				return
			}
			goal := "g3"
			if c.name == "ml-del2-both-edit3" || c.name == "sec-del2-text-edit-headerB" {
				goal = "g2"
			}
			if c.name == "delete-text-rule1" {
				goal = "g1"
			}
			if !strings.Contains(msg, "has no rule text") || !strings.Contains(msg, `"  Зачем: `+goal+`"`) {
				t.Fatalf("refusal = %q", msg)
			}
			for _, other := range []string{"g1", "g2", "g3", "g4"} {
				if other != goal && strings.Contains(msg, `"  Зачем: `+other+`"`) {
					t.Fatalf("refusal = %q", msg)
				}
			}
		})
	}
}

func TestAbsorbRefusesTextAboveHeader(t *testing.T) {
	cases := []struct {
		name, prefix string
		edit         bool
	}{{"one-line", "intro\n", false}, {"blank-line", "note\n\n", false}, {"edit-below", "intro\n", true}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			out := filepath.Join(dir, "a.md")
			codex := filepath.Join(dir, "cx")
			data := []byte("# A\n\n- one\n\n- two\n")
			mustWrite(t, out, data)
			if err := importData(codex, out, "a.md", data, false); err != nil {
				t.Fatal(err)
			}
			path := manifestOf(t, codex, "a.md")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			mustWrite(t, path, bytes.Replace(raw, []byte("header: false\n"), []byte("header: true\n"), 1))
			if err := BuildWithOptions(codex, BuildOptions{Force: true}); err != nil {
				t.Fatal(err)
			}
			data, err = os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if c.edit {
				data = bytes.Replace(data, []byte("- two\n"), []byte("- two edited\n"), 1)
			}
			data = append([]byte(c.prefix), data...)
			mustWrite(t, out, data)
			before := treeBytes(t, codex)
			err = Absorb(codex, out, AbsorbOptions{DraftDir: codex})
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), "header") || !strings.Contains(err.Error(), "below") {
				t.Fatalf("refusal = %v", err)
			}
			after, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(data, after) || !bytes.Equal(before, treeBytes(t, codex)) {
				t.Fatal("refusal changed file or codex")
			}
		})
	}
}
