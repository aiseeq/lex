package lex

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func ruleCodeFixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	root := t.TempDir()
	codex, out := filepath.Join(root, "codex"), filepath.Join(root, "out.md")
	body := []byte("first rule\n\nsecond rule\n")
	if err := importData(codex, out, "rules.md", body, false); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, out, body)
	m, err := readManifest(filepath.Join(codex, "manifest", "rules.md.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	frag := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md")
	card := filepath.Join(root, "card.md")
	mustWrite(t, card, []byte("mechanism\n"))
	return codex, out, frag, card
}

func linkRule(t *testing.T, codex, frag, target string) {
	t.Helper()
	b, err := os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	b = bytes.Replace(b, []byte("снять_когда: \n"), []byte("снять_когда: \nкод: "+target+"\n"), 1)
	mustWrite(t, frag, b)
	if err := ConfirmCodeLinks(codex, frag); err != nil {
		t.Fatal(err)
	}
}

func TestRuleCodeSilent(t *testing.T) {
	codex, _, frag, card := ruleCodeFixture(t)
	linkRule(t, codex, frag, card)
	w, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil || len(w) != 0 {
		t.Fatalf("warnings %v, error %v", w, err)
	}
}

func TestRuleCodeTargetChanged(t *testing.T) {
	for _, kind := range []string{"card", "test"} {
		for _, removal := range []bool{false, true} {
			t.Run(kind+string(rune('0'+btoi(removal))), func(t *testing.T) {
				codex, _, frag, target := ruleCodeFixture(t)
				file := target
				if kind == "test" {
					repo := t.TempDir()
					file = filepath.Join(repo, "rule_test.go")
					mustWrite(t, file, []byte("package sample\nfunc TestRule(t *testing.T) {}\nfunc TestNeighbor(t *testing.T) {}\n"))
					target = repo + ":.:TestRule"
				}
				original, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				linkRule(t, codex, frag, target)
				if removal {
					if err := os.Remove(file); err != nil {
						t.Fatal(err)
					}
				} else {
					changed := append(bytes.Clone(original), []byte("// change\n")...)
					if kind == "test" {
						changed = bytes.Replace(original, []byte("TestRule(t *testing.T) {}"), []byte("TestRule(t *testing.T) { t.Log(1) }"), 1)
					}
					mustWrite(t, file, changed)
				}
				w, err := CheckWithWarnings(codex, CheckOptions{})
				reason := "target changed"
				if removal {
					reason = "target not found"
				}
				if err != nil || len(w) != 1 || !strings.Contains(w[0], frag) || !strings.Contains(w[0], target) || !strings.Contains(w[0], reason) {
					t.Fatalf("warnings %v, error %v", w, err)
				}
				if removal {
					mustWrite(t, file, original)
				}
				if err := ConfirmCodeLinks(codex, frag); err != nil {
					t.Fatal(err)
				}
				w, err = CheckWithWarnings(codex, CheckOptions{})
				if err != nil || len(w) != 0 {
					t.Fatalf("warnings %v, error %v", w, err)
				}
			})
		}
	}
	t.Run("stamp only", func(t *testing.T) {
		codex, _, frag, card := ruleCodeFixture(t)
		mustWrite(t, card, []byte("card\n<!-- stamp: v5 file@1 -->\ncontents\n"))
		linkRule(t, codex, frag, card)
		mustWrite(t, card, []byte("card\n<!-- stamp: v5 file@2 -->\ncontents\n"))
		w, err := CheckWithWarnings(codex, CheckOptions{})
		if err != nil || len(w) != 0 {
			t.Fatalf("stamp changed: warnings %v, error %v", w, err)
		}
	})
	t.Run("neighbor test only", func(t *testing.T) {
		codex, _, frag, _ := ruleCodeFixture(t)
		repo := t.TempDir()
		file := filepath.Join(repo, "rule_test.go")
		original := []byte("package sample\nfunc TestRule(t *testing.T) {}\nfunc TestNeighbor(t *testing.T) {}\n")
		mustWrite(t, file, original)
		linkRule(t, codex, frag, repo+":.:TestRule")
		mustWrite(t, file, bytes.Replace(original, []byte("TestNeighbor(t *testing.T) {}"), []byte("TestNeighbor(t *testing.T) { t.Log(1) }"), 1))
		w, err := CheckWithWarnings(codex, CheckOptions{})
		if err != nil || len(w) != 0 {
			t.Fatalf("neighbor changed: warnings %v, error %v", w, err)
		}
	})
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestRuleCodeTextChanged(t *testing.T) {
	codex, out, frag, card := ruleCodeFixture(t)
	linkRule(t, codex, frag, card)
	b, err := os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, frag, bytes.Replace(b, []byte("first rule"), []byte("edited rule"), 1))
	mustWrite(t, out, []byte("edited rule\n\nsecond rule\n"))
	w, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil || len(w) != 1 || !strings.Contains(w[0], card) || !strings.Contains(w[0], "rule text changed") {
		t.Fatalf("warnings %v, error %v", w, err)
	}
	if err := ConfirmCodeLinks(codex, frag); err != nil {
		t.Fatal(err)
	}
	w, err = CheckWithWarnings(codex, CheckOptions{})
	if err != nil || len(w) != 0 {
		t.Fatalf("warnings %v, error %v", w, err)
	}
}

func TestRuleCodeUnconfirmed(t *testing.T) {
	codex, _, frag, card := ruleCodeFixture(t)
	b, err := os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, frag, bytes.Replace(b, []byte("снять_когда: \n"), []byte("снять_когда: \nкод: "+card+"\n"), 1))
	w, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil || len(w) != 1 || !strings.Contains(w[0], frag) || !strings.Contains(w[0], card) || !strings.Contains(w[0], "link unconfirmed") {
		t.Fatalf("warnings %v, error %v", w, err)
	}
}

func TestRuleRecord(t *testing.T) {
	codex, out, _, card := ruleCodeFixture(t)
	record := filepath.Join(codex, "frag", "rules.md", "record.md")
	raw := []byte("---\nid: rules.md/record\nцель: intent\nоснование: reason\nснять_когда: \nтип: правило\nзапись: true\nкод: " + card + "\n---\nsecret record\n")
	mustWrite(t, record, raw)
	if err := ConfirmCodeLinks(codex, record); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, true); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("secret record")) {
		t.Fatal("record built")
	}
	if err := importData(codex, out, "rules.md", []byte("first rule\n\nsecond rule\n\nthird rule\n"), true); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, out, []byte("first rule\n\nsecond rule\n\nthird rule\n"))
	root := filepath.Dir(codex)
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %s %v", b, err)
		}
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	if _, err := CheckWithWarnings(codex, CheckOptions{Staged: true}); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, record, bytes.Replace(b, []byte("запись: true\n"), nil, 1))
	if _, err := CheckWithWarnings(codex, CheckOptions{}); err == nil {
		t.Fatal("unmarked orphan accepted")
	}
}

func TestRuleRecordNotFromRemoval(t *testing.T) {
	codex, out, _, _ := ruleCodeFixture(t)
	mustWrite(t, out, []byte("first rule\n"))
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(codex, "frag", "rules.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		b, err := os.ReadFile(filepath.Join(codex, "frag", "rules.md", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if fragmentIsRecord(b) {
			t.Fatal("absorb created record")
		}
	}
}

func TestRuleCodeRefresh(t *testing.T) {
	codex, out, frag, card := ruleCodeFixture(t)
	linkRule(t, codex, frag, card)
	if err := importData(codex, out, "rules.md", []byte("insert\n\nfirst rule\n\nsecond rule\n"), true); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	links, hash := codeLinks(b)
	if len(links) != 1 || links[0].Hash == "" || hash == "" {
		t.Fatalf("lost link %v %q", links, hash)
	}
	if err := importData(codex, out, "rules.md", []byte("insert\n\nsecond rule\n"), true); err != nil {
		t.Fatal(err)
	}
	debt, err := CodeDebt(codex)
	if err != nil || len(debt) != 1 || !strings.Contains(debt[0], card) || !strings.Contains(debt[0], "first-rule") {
		t.Fatalf("debt %v %v", debt, err)
	}
}

func TestRuleCodeDebt(t *testing.T) {
	codex, _, frag, card := ruleCodeFixture(t)
	linkRule(t, codex, frag, card)
	debt, err := CodeDebt(codex)
	if err != nil || len(debt) != 0 {
		t.Fatalf("debt %v %v", debt, err)
	}
	mustWrite(t, card, []byte("changed\n"))
	debt, err = CodeDebt(codex)
	if err != nil || len(debt) != 1 || !strings.Contains(debt[0], frag) || !strings.Contains(debt[0], card) {
		t.Fatalf("debt %v %v", debt, err)
	}
}

func TestRuleCodeDebtRestored(t *testing.T) {
	codex, out, frag, card := ruleCodeFixture(t)
	linkRule(t, codex, frag, card)
	linked, err := os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if err := importData(codex, out, "rules.md", []byte("second rule\n"), true); err != nil {
			t.Fatal(err)
		}
		debt, err := CodeDebt(codex)
		if err != nil || len(debt) != 1 || !strings.Contains(debt[0], card) {
			t.Fatalf("refresh %d: debt %v, error %v", i, debt, err)
		}
		if err := importData(codex, out, "rules.md", []byte("first rule\n\nsecond rule\n"), true); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			mustWrite(t, frag, linked)
			mp := filepath.Join(codex, "manifest", "rules.md.yaml")
			m, err := readManifest(mp)
			if err != nil {
				t.Fatal(err)
			}
			m.fragmentHashes["rules.md/first-rule-1"] = sha(linked)
			if err := writeManifest(mp, m, m.links); err != nil {
				t.Fatal(err)
			}
		}
	}
	linkRule(t, codex, frag, card)
	debt, err := CodeDebt(codex)
	if err != nil || len(debt) != 0 {
		t.Fatalf("confirmed: debt %v, error %v", debt, err)
	}
}

func TestRuleCodeDebtRestoredDifferentFragment(t *testing.T) {
	codex, out, frag, card := ruleCodeFixture(t)
	linkRule(t, codex, frag, card)
	if err := importData(codex, out, "rules.md", []byte("rewritten rule\n\nsecond rule\n"), true); err != nil {
		t.Fatal(err)
	}
	debt, err := CodeDebt(codex)
	if err != nil || len(debt) != 1 || !strings.Contains(debt[0], card) {
		t.Fatalf("lost: debt %v, error %v", debt, err)
	}
	newFrag := filepath.Join(codex, "frag", "rules.md", "rewritten-rule-1.md")
	linkRule(t, codex, newFrag, card)
	debt, err = CodeDebt(codex)
	if err != nil || len(debt) != 0 {
		t.Fatalf("confirmed on a different fragment: debt %v, error %v", debt, err)
	}
}

func TestRuleCodeTargetMissingTestAndPackage(t *testing.T) {
	for _, missing := range []string{"test", "package"} {
		t.Run(missing, func(t *testing.T) {
			codex, _, frag, _ := ruleCodeFixture(t)
			repo := t.TempDir()
			dir := filepath.Join(repo, "pkg")
			if err := os.MkdirAll(dir, directoryMode); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, "rule_test.go")
			mustWrite(t, file, []byte("package pkg\nfunc TestRule(t *testing.T) {}\nfunc TestOther(t *testing.T) {}\n"))
			target := repo + ":pkg:TestRule"
			linkRule(t, codex, frag, target)
			if missing == "test" {
				mustWrite(t, file, []byte("package pkg\nfunc TestOther(t *testing.T) {}\n"))
			} else if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
			warnings, err := CheckWithWarnings(codex, CheckOptions{})
			if err != nil || len(warnings) != 1 || !strings.Contains(warnings[0], "target not found") || !strings.Contains(warnings[0], target) || !strings.Contains(warnings[0], frag) {
				t.Fatalf("warnings %v, error %v", warnings, err)
			}
		})
	}
}
