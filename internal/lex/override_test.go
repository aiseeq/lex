package lex

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func overrideFixture(t *testing.T) (string, string, string, string, string) {
	t.Helper()
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	rules := filepath.Join(root, "rules.md")
	amend := filepath.Join(root, "amend.md")
	ruleBody := []byte("- Правило первое\n- Правило второе\n")
	amendBody := []byte("- Поправка первая\n- Поправка вторая\n")
	for _, f := range []struct {
		path, rel string
		body      []byte
	}{{rules, "rules.md", ruleBody}, {amend, "amend.md", amendBody}} {
		mustWrite(t, f.path, f.body)
		if err := importData(codex, f.path, f.rel, f.body, false); err != nil {
			t.Fatal(err)
		}
	}
	return codex, rules, amend, "rules.md/fragment-1", "amend.md/fragment-1"
}

func overridePath(codex, id string) string {
	return filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
}
func addOverride(t *testing.T, codex, amend, target string, reason bool) {
	t.Helper()
	p := overridePath(codex, amend)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if reason {
		raw = bytes.Replace(raw, []byte("цель: \n"), []byte("цель: причина\n"), 1)
		raw = bytes.Replace(raw, []byte("основание: \n"), []byte("основание: источник\n"), 1)
		raw = bytes.Replace(raw, []byte("снять_когда: \n"), []byte("снять_когда: условие\n"), 1)
	}
	remove := "снять_когда: \n"
	if reason {
		remove = "снять_когда: условие\n"
	}
	raw = bytes.Replace(raw, []byte(remove), []byte(remove+"переопределяет: "+target+"\n"), 1)
	mustWrite(t, p, raw)
}
func overrideCheck(t *testing.T, codex, want string) {
	t.Helper()
	w, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil || (want == "" && len(w) != 0) || (want != "" && !strings.Contains(strings.Join(w, "\n"), want)) {
		t.Fatalf("warnings %v, err %v, want %q", w, err, want)
	}
}
func refreshOverride(t *testing.T, codex, out, rel, source string) {
	t.Helper()
	mustWrite(t, out, []byte(source))
	if err := importData(codex, out, rel, []byte(source), true); err != nil {
		t.Fatal(err)
	}
}

func capturedRefresh(t *testing.T, codex, out, rel, source string) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stderr
	os.Stderr = writer
	mustWrite(t, out, []byte(source))
	refreshErr := importData(codex, out, rel, []byte(source), true)
	os.Stderr = previous
	writer.Close()
	output, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || refreshErr != nil {
		t.Fatalf("refresh: %v; read: %v", refreshErr, err)
	}
	return string(output)
}

func TestOverrideSilent(t *testing.T) {
	c, _, _, rule, amendment := overrideFixture(t)
	addOverride(t, c, amendment, rule, true)
	if err := ConfirmOverrides(c, amendment); err != nil {
		t.Fatal(err)
	}
	overrideCheck(t, c, "")
	if err := Build(c, true); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		body, err := os.ReadFile(overridePath(c, amendment))
		if err != nil {
			t.Fatal(err)
		}
		part, err := fragmentBody(body)
		if err != nil {
			t.Fatal(err)
		}
		built, _, err := renderPurpose(amendment, body, part, enabled)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(built, []byte("переопределяет")) {
			t.Fatal(string(built))
		}
	}
}

func TestOverrideBuildPurpose(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		c, out, mp, m := purposeFixture(t, "- first\n- second\n")
		purposeSet(t, c, enabled)
		purposeFragment(t, c, mp, m, 0, "reason")
		if err := Build(c, false); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		addOverride(t, c, m.parts[0], m.parts[1], false)
		if err := ConfirmOverrides(c, m.parts[0]); err != nil {
			t.Fatal(err)
		}
		if err := Build(c, false); err != nil {
			t.Fatal(err)
		}
		after, err := os.ReadFile(out)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("purpose %v: output changed: %q / %q, %v", enabled, before, after, err)
		}
	}
}
func TestOverrideReasonEmpty(t *testing.T) {
	c, _, _, r, a := overrideFixture(t)
	addOverride(t, c, a, r, false)
	if err := ConfirmOverrides(c, a); err != nil {
		t.Fatal(err)
	}
	overrideCheck(t, c, "пусты цель и основание")
}
func TestOverrideUnconfirmed(t *testing.T) {
	c, _, _, r, a := overrideFixture(t)
	addOverride(t, c, a, r, true)
	overrideCheck(t, c, "связь не подтверждена")
}
func TestOverrideRuleChanged(t *testing.T) {
	c, rules, _, r, a := overrideFixture(t)
	addOverride(t, c, a, r, true)
	if err := ConfirmOverrides(c, a); err != nil {
		t.Fatal(err)
	}
	p := overridePath(c, r)
	raw, _ := os.ReadFile(p)
	mustWrite(t, p, bytes.Replace(raw, []byte("первое"), []byte("новое"), 1))
	mustWrite(t, rules, []byte("- Правило новое\n- Правило второе\n"))
	overrideCheck(t, c, "текст правила изменился")
	if err := ConfirmOverrides(c, a); err != nil {
		t.Fatal(err)
	}
	overrideCheck(t, c, "")
}
func TestOverrideMissingID(t *testing.T) {
	c, _, _, _, a := overrideFixture(t)
	addOverride(t, c, a, "rules.md/missing-1", true)
	overrideCheck(t, c, "id не найден")
	if ConfirmOverrides(c, a) == nil {
		t.Fatal("confirmed missing target")
	}
	overrideCheck(t, c, "id не найден")
}
func TestOverrideCount(t *testing.T) {
	c, _, _, r, a := overrideFixture(t)
	entries, err := CountOverrides(c)
	if err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
	addOverride(t, c, a, r, true)
	addOverride(t, c, a, "rules.md/fragment-2", true)
	addOverride(t, c, "amend.md/fragment-2", r, true)
	third := []byte("- Третья поправка\n")
	out := filepath.Join(filepath.Dir(c), "third.md")
	mustWrite(t, out, third)
	if err := importData(c, out, "third.md", third, false); err != nil {
		t.Fatal(err)
	}
	addOverride(t, c, "third.md/fragment-1", r, true)
	entries, err = CountOverrides(c)
	if err != nil || len(entries) != 2 || !strings.HasPrefix(entries[0], r+": 3") || !strings.Contains(entries[1], "fragment-2: 1") || !strings.Contains(entries[0], "amend.md") {
		t.Fatal(entries, err)
	}
}

func TestOverrideCountSkipsBroken(t *testing.T) {
	c, _, _, r, a := overrideFixture(t)
	addOverride(t, c, a, r, true)
	raw, err := os.ReadFile(overridePath(c, a))
	if err != nil {
		t.Fatal(err)
	}
	links := overrideLinks(raw)
	links[0].broken = true
	mustWrite(t, overridePath(c, a), setOverrideLinks(raw, links))
	entries, err := CountOverrides(c)
	if err != nil || len(entries) != 0 {
		t.Fatalf("оборванная связь не входит в счёт: %v, %v", entries, err)
	}
}

func TestOverrideRefreshChangedAtSameID(t *testing.T) {
	c, rules, _, r, a := overrideFixture(t)
	addOverride(t, c, a, r, true)
	if err := ConfirmOverrides(c, a); err != nil {
		t.Fatal(err)
	}
	refreshOverride(t, c, rules, "rules.md", "- Правило изменено\n- Правило второе\n")
	raw, err := os.ReadFile(overridePath(c, a))
	if err != nil || overrideLinks(raw)[0].broken || overrideLinks(raw)[0].target != r {
		t.Fatalf("link changed: %s, %v", raw, err)
	}
	overrideCheck(t, c, "текст правила изменился")
}

func TestOverrideRefreshRemoved(t *testing.T) {
	c, rules, _, _, a := overrideFixture(t)
	r := "rules.md/fragment-2"
	addOverride(t, c, a, r, true)
	if err := ConfirmOverrides(c, a); err != nil {
		t.Fatal(err)
	}
	refreshOverride(t, c, rules, "rules.md", "- Правило первое\n")
	overrideCheck(t, c, "id не найден")
}
func TestOverrideRefreshRenamed(t *testing.T) {
	c, rules, amend, r, a := overrideFixture(t)
	addOverride(t, c, a, r, true)
	if err := ConfirmOverrides(c, a); err != nil {
		t.Fatal(err)
	}
	if output := capturedRefresh(t, c, rules, "rules.md", "- Новое правило\n- Правило первое\n- Правило второе\n"); !strings.Contains(output, a) {
		t.Fatalf("refresh did not name amendment: %q", output)
	}
	raw, _ := os.ReadFile(overridePath(c, a))
	links := overrideLinks(raw)
	if len(links) != 1 || links[0].target != "rules.md/fragment-2" || links[0].hash == "" {
		t.Fatal(links)
	}
	overrideCheck(t, c, "")
	refreshOverride(t, c, amend, "amend.md", "- Поправка первая\n- Поправка вторая изменена\n")
	raw, _ = os.ReadFile(overridePath(c, a))
	if overrideLinks(raw)[0].target != "rules.md/fragment-2" {
		t.Fatal(string(raw))
	}
}
func TestOverrideRefreshInsert(t *testing.T) {
	c, rules, amend, r, a := overrideFixture(t)
	addOverride(t, c, a, r, true)
	if err := ConfirmOverrides(c, a); err != nil {
		t.Fatal(err)
	}
	refreshOverride(t, c, rules, "rules.md", "- Новое\n- Правило первое\n- Правило второе\n")
	refreshOverride(t, c, amend, "amend.md", "- Новая\n- Поправка первая\n- Поправка вторая\n")
	raw, err := os.ReadFile(overridePath(c, "amend.md/fragment-2"))
	if err != nil || len(overrideLinks(raw)) != 1 || overrideLinks(raw)[0].target != "rules.md/fragment-2" {
		t.Fatal(string(raw), err)
	}
	overrideCheck(t, c, "")
}

func TestOverrideRefreshNamedSlug(t *testing.T) {
	root := t.TempDir()
	c := filepath.Join(root, "codex")
	rules := filepath.Join(root, "rules.md")
	amend := filepath.Join(root, "amend.md")
	base := strings.Repeat("a", 48)
	source := "- " + base + " first\n- " + base + " second\n"
	amendments := "- amendment\n"
	mustWrite(t, rules, []byte(source))
	mustWrite(t, amend, []byte(amendments))
	if err := importData(c, rules, "rules.md", []byte(source), false); err != nil {
		t.Fatal(err)
	}
	if err := importData(c, amend, "amend.md", []byte(amendments), false); err != nil {
		t.Fatal(err)
	}
	rule := "rules.md/" + base + "-1"
	a := "amend.md/amendment-1"
	addOverride(t, c, a, rule, true)
	if err := ConfirmOverrides(c, a); err != nil {
		t.Fatal(err)
	}
	refreshOverride(t, c, rules, "rules.md", "- "+base+" new\n"+source)
	raw, err := os.ReadFile(overridePath(c, a))
	if err != nil || overrideLinks(raw)[0].target != "rules.md/"+base+"-2" {
		t.Fatalf("renamed link: %s, %v", raw, err)
	}
	overrideCheck(t, c, "")
}
func TestOverrideRefreshBroken(t *testing.T) {
	c, rules, amend, r, a := overrideFixture(t)
	addOverride(t, c, a, r, true)
	if err := ConfirmOverrides(c, a); err != nil {
		t.Fatal(err)
	}
	if output := capturedRefresh(t, c, rules, "rules.md", "- Правило второе\n"); !strings.Contains(output, a) {
		t.Fatalf("refresh did not name broken amendment: %q", output)
	}
	overrideCheck(t, c, "обрыв связи")
	if ConfirmOverrides(c, a) == nil {
		t.Fatal("confirmed broken link")
	}
	refreshOverride(t, c, amend, "amend.md", "- Поправка первая\n- Поправка вторая изменена\n")
	overrideCheck(t, c, "обрыв связи")
}
func TestOverrideRefreshOwnTopic(t *testing.T) {
	c, _, amend, r, a := overrideFixture(t)
	addOverride(t, c, a, r, true)
	if err := ConfirmOverrides(c, a); err != nil {
		t.Fatal(err)
	}
	refreshOverride(t, c, amend, "amend.md", "- Поправка первая\n- Поправка другая\n")
	raw, _ := os.ReadFile(overridePath(c, a))
	if l := overrideLinks(raw); len(l) != 1 || l[0].hash == "" {
		t.Fatal(l)
	}
}
func TestOverrideHandConfirm(t *testing.T) {
	c, _, amend, r, a := overrideFixture(t)
	before, _ := os.ReadFile(amend)
	addOverride(t, c, a, r, true)
	if err := ConfirmOverrides(c, a); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(amend)
	if !bytes.Equal(before, after) {
		t.Fatal("output changed")
	}
	overrideCheck(t, c, "")
	refreshOverride(t, c, amend, "amend.md", "- Поправка первая\n- Поправка другая\n")
}
func TestOverrideNoHashParts(t *testing.T) {
	c, rules, _, r, a := overrideFixture(t)
	mp := filepath.Join(c, "manifest", "amend.md.yaml")
	m, err := readManifest(mp)
	if err != nil {
		t.Fatal(err)
	}
	makePartsUnhashed(t, mp, m, m.parts[1])
	addOverride(t, c, a, r, true)
	if err := ConfirmOverrides(c, a); err != nil {
		t.Fatal(err)
	}
	assertNoHashLine(t, mp, m.parts[1])
	if _, err := readManifest(mp); err != nil {
		t.Fatal(err)
	}
	refreshOverride(t, c, rules, "rules.md", "- Новое\n- Правило первое\n- Правило второе\n")
	assertNoHashLine(t, mp, m.parts[1])
	if _, err := readManifest(mp); err != nil {
		t.Fatal(err)
	}
	refreshOverride(t, c, rules, "rules.md", "- Правило второе\n- Новое\n")
	raw, err := os.ReadFile(overridePath(c, a))
	if err != nil || !overrideLinks(raw)[0].broken {
		t.Fatalf("broken link not recorded: %s, %v", raw, err)
	}
	assertNoHashLine(t, mp, m.parts[1])
	if _, err := readManifest(mp); err != nil {
		t.Fatal(err)
	}
}
