package lex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func roleBudgetFixture(t *testing.T) (string, []string) {
	t.Helper()
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	if err := os.MkdirAll(filepath.Join(codex, "manifest"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(codex, "frag"), 0o755); err != nil {
		t.Fatal(err)
	}
	ids := []string{"shared", "second", "alpha-rule", "git-beta-rule", "work-beta-rule", "work-beta-rules-rule", "orphan-rule", "ordinary"}
	for _, id := range ids {
		raw := []byte("---\nid: " + id + "\nцель: цель\nоснование: причина\nснять_когда: условие\nтип: правило\n---\nТекст правила.\n")
		if err := os.WriteFile(filepath.Join(codex, "frag", id+".md"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name string, parts ...string) {
		t.Helper()
		m := manifest{out: filepath.Join(root, name+".md"), hash: "source", header: true, fragmentHashes: map[string]string{}}
		m.parts = parts
		for _, id := range parts {
			raw, err := os.ReadFile(filepath.Join(codex, "frag", id+".md"))
			if err != nil {
				t.Fatal(err)
			}
			m.fragmentHashes[id] = sha(raw)
		}
		manifestPath := filepath.Join(codex, "manifest", name+".yaml")
		if err := os.WriteFile(manifestPath, manifestContent(m, nil), 0o644); err != nil {
			t.Fatal(err)
		}
		body, err := manifestBody(codex, m)
		if err != nil {
			t.Fatal(err)
		}
		full := generatedHeader(manifestPath, body)
		m.builtHash = sha(full)
		if err := writeManifest(manifestPath, m, nil); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(m.out, full, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("git-alpha-CLAUDE.md", "shared")
	write("work-alpha-CLAUDE.md", "shared")
	write("work-alpha-.claude-rules-main", "alpha-rule")
	write("git-beta-CLAUDE.md", "shared", "git-beta-rule")
	write("work-beta-CLAUDE.md", "work-beta-rule")
	write("work-beta-.claude-rules-main", "work-beta-rules-rule")
	write("git-orphan-.claude-rules-main", "orphan-rule")
	write("ordinary", "ordinary")
	write("work-CLAUDE.md", "second")
	return codex, ids
}

func TestRoleBuildsBudgetAndProjectExpansion(t *testing.T) {
	codex, _ := roleBudgetFixture(t)
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(RuleWarningSummary(warnings), "сборок сверх предела роли") {
		t.Fatalf("summary changed without roles.tsv: %s", RuleWarningSummary(warnings))
	}
	roles := "роль\tпредел\tманифесты\nмастер\t1\tgit-alpha-CLAUDE.md git-beta-CLAUDE.md\nпроект\t1\t{project} {project-rules}\nниже\t2\tgit-alpha-CLAUDE.md\n"
	if err := os.WriteFile(filepath.Join(codex, "roles.tsv"), []byte(roles), 0o644); err != nil {
		t.Fatal(err)
	}
	builds, err := RoleBuilds(codex)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]RoleBuild{}
	for _, build := range builds {
		got[build.Role+"/"+build.Project] = build
	}
	if got["мастер/"].Rules != 2 || got["мастер/"].Limit != 1 {
		t.Fatalf("shared fragment was not deduplicated: %#v", got["мастер/"])
	}
	for key, want := range map[string]int{
		"проект/git-alpha": 1, "проект/work-alpha": 2,
		"проект/git-beta": 2, "проект/work-beta": 2,
	} {
		if got[key].Rules != want {
			t.Errorf("%s rules = %d, want %d (%#v)", key, got[key].Rules, want, got[key])
		}
	}
	if _, exists := got["проект/git-orphan"]; exists {
		t.Fatal("rules without a project CLAUDE manifest created a role build")
	}
	if got["ниже/"].Rules != 1 || got["ниже/"].Limit != 2 {
		t.Fatalf("below-limit build = %#v", got["ниже/"])
	}
	warnings, err = CheckWithWarnings(codex, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "роль мастер: правил 2, предел 1") || !strings.Contains(joined, "роль проект проект work-alpha: правил 2, предел 1") || strings.Contains(joined, "роль ниже") || !strings.Contains(RuleWarningSummary(warnings), "сборок сверх предела роли: 4") {
		t.Fatalf("missing over-budget warning or count: %v; %s", warnings, RuleWarningSummary(warnings))
	}
}

func TestRoleBuildsKeepsExplicitManifestWhenProjectTokensDoNotExpand(t *testing.T) {
	codex, _ := roleBudgetFixture(t)
	for _, name := range []string{
		"git-alpha-CLAUDE.md", "work-alpha-CLAUDE.md", "git-beta-CLAUDE.md", "work-beta-CLAUDE.md",
	} {
		if err := os.Remove(filepath.Join(codex, "manifest", name+".yaml")); err != nil {
			t.Fatal(err)
		}
	}
	roles := "роль\tпредел\tманифесты\nмастер\t256\tordinary {project} {project-rules}\n"
	if err := os.WriteFile(filepath.Join(codex, "roles.tsv"), []byte(roles), 0o644); err != nil {
		t.Fatal(err)
	}
	builds, err := RoleBuilds(codex)
	if err != nil {
		t.Fatal(err)
	}
	if len(builds) != 1 || builds[0].Role != "мастер" || builds[0].Project != "" || builds[0].Rules != 1 {
		t.Fatalf("explicit manifest build was lost or project-only manifests created builds: %#v", builds)
	}
}

func TestRoleBuildBelowLimitHasNoWarning(t *testing.T) {
	codex, _ := roleBudgetFixture(t)
	roles := "роль\tпредел\tманифесты\nмастер\t2\tgit-alpha-CLAUDE.md\n"
	if err := os.WriteFile(filepath.Join(codex, "roles.tsv"), []byte(roles), 0o644); err != nil {
		t.Fatal(err)
	}
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(warnings, "\n"), roleBudgetWarning) || !strings.Contains(RuleWarningSummary(warnings), "сборок сверх предела роли: 0") {
		t.Fatalf("below-limit check warnings = %v; summary = %s", warnings, RuleWarningSummary(warnings))
	}
}

func TestCheckWithMalformedRolesDoesNotReportRoleBudgetSummary(t *testing.T) {
	codex, _ := roleBudgetFixture(t)
	if err := os.WriteFile(filepath.Join(codex, "roles.tsv"), []byte("bad header\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err == nil || !strings.Contains(err.Error(), "invalid roles.tsv header") {
		t.Fatalf("CheckWithWarnings error = %v, want invalid roles.tsv header", err)
	}
	if strings.Contains(RuleWarningSummary(warnings), "сборок сверх предела роли") {
		t.Fatalf("failed roles.tsv check emitted role budget summary: %s", RuleWarningSummary(warnings))
	}
}

func TestRoleBuildsUnknownTokenIsFormatError(t *testing.T) {
	codex, _ := roleBudgetFixture(t)
	roles := "роль\tпредел\tманифесты\nмастер\t256\t{unknown}\n"
	if err := os.WriteFile(filepath.Join(codex, "roles.tsv"), []byte(roles), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RoleBuilds(codex); err == nil || !strings.Contains(err.Error(), "unknown manifest token") {
		t.Fatalf("RoleBuilds error = %v, want unknown token format error", err)
	}
}
