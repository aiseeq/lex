package lex

import (
	"bufio"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const roleBudgetWarning = ": сборка сверх предела роли"
const roleBudgetEnabledNote = "lex: проверка бюджета ролей включена"

type RoleBuild struct {
	Role    string
	Project string
	Rules   int
	Limit   int
}

// RoleBudgetWarning formats the warning for an over-limit role build.
func RoleBudgetWarning(build RoleBuild) string {
	project := ""
	if build.Project != "" {
		project = " проект " + build.Project
	}
	return fmt.Sprintf("роль %s%s: правил %d, предел %d%s", build.Role, project, build.Rules, build.Limit, roleBudgetWarning)
}

// RoleBudgetSummary returns the over-limit build count for a set of role builds.
func RoleBudgetSummary(builds []RoleBuild) string {
	overLimit := 0
	for _, build := range builds {
		if build.Rules > build.Limit {
			overLimit++
		}
	}
	return fmt.Sprintf("сборок сверх предела роли: %d", overLimit)
}

type roleSpec struct {
	role      string
	limit     int
	manifests []string
}

var projectManifestRE = regexp.MustCompile(`^(?:git|work)-(.+)-CLAUDE\.md$`)
var projectRulesRE = regexp.MustCompile(`^(.+)-\.claude-rules-.+$`)

// RoleBuilds reads optional roles.tsv and counts unique rule fragments in each role build.
func RoleBuilds(codex string) ([]RoleBuild, error) {
	codex, err := canonicalCodex(codex, false)
	if err != nil {
		return nil, err
	}
	rolesPath := filepath.Join(codex, "roles.tsv")
	data, err := os.ReadFile(rolesPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: read roles.tsv: %w", rolesPath, err)
	}
	specs, err := parseRoleSpecs(rolesPath, data)
	if err != nil {
		return nil, err
	}
	catalog, err := listManifestCatalog(codex)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]string, len(catalog.paths))
	for _, path := range catalog.paths {
		byName[strings.TrimSuffix(filepath.Base(path), ".yaml")] = path
	}
	var builds []RoleBuild
	for _, spec := range specs {
		groups, err := roleManifestGroups(spec, catalog, rolesPath, byName)
		if err != nil {
			return nil, err
		}
		for _, project := range slices.Sorted(maps.Keys(groups)) {
			rules, err := countManifestRules(codex, catalog, byName, groups[project])
			if err != nil {
				return nil, err
			}
			builds = append(builds, RoleBuild{Role: spec.role, Project: project, Rules: rules, Limit: spec.limit})
		}
	}
	return builds, nil
}

func roleManifestGroups(spec roleSpec, catalog *manifestCatalog, rolesPath string, byName map[string]string) (map[string]map[string]bool, error) {
	groups := map[string]map[string]bool{}
	projectGroups := map[string]map[string]bool{}
	hasProjectToken := false
	for _, token := range spec.manifests {
		if token == "{project}" || token == "{project-rules}" {
			hasProjectToken = true
			addProjectTokenGroups(projectGroups, catalog, token)
			continue
		}
		if _, ok := byName[token]; !ok {
			return nil, fmt.Errorf("%s: manifest %q does not exist", rolesPath, token)
		}
		addManifest(groups, "", token)
	}
	if !hasProjectToken {
		return groups, nil
	}
	if len(projectGroups) == 0 {
		return groups, nil
	}
	literal := groups[""]
	groups = make(map[string]map[string]bool, len(projectGroups))
	for project, names := range projectGroups {
		for name := range literal {
			names[name] = true
		}
		groups[project] = names
	}
	return groups, nil
}

func addProjectTokenGroups(groups map[string]map[string]bool, catalog *manifestCatalog, token string) {
	projects := projectManifestNames(catalog)
	if token == "{project}" {
		for project, name := range projects {
			addManifest(groups, project, name)
		}
		return
	}
	for _, path := range catalog.paths {
		name := strings.TrimSuffix(filepath.Base(path), ".yaml")
		project := projectForManifest(name, token)
		if project != "" {
			if _, hasProjectManifest := projects[project]; !hasProjectManifest {
				continue
			}
			addManifest(groups, project, name)
		}
	}
}

func projectManifestNames(catalog *manifestCatalog) map[string]string {
	projects := make(map[string]string)
	for _, path := range catalog.paths {
		name := strings.TrimSuffix(filepath.Base(path), ".yaml")
		if project := projectForManifest(name, "{project}"); project != "" {
			projects[project] = name
		}
	}
	return projects
}

func projectForManifest(name, token string) string {
	if token == "{project}" {
		if projectManifestRE.FindStringSubmatch(name) == nil {
			return ""
		}
		return strings.TrimSuffix(name, "-CLAUDE.md")
	}
	match := projectRulesRE.FindStringSubmatch(name)
	if match == nil {
		return ""
	}
	return match[1]
}

func addManifest(groups map[string]map[string]bool, project, name string) {
	if groups[project] == nil {
		groups[project] = map[string]bool{}
	}
	groups[project][name] = true
}

func countManifestRules(codex string, catalog *manifestCatalog, byName map[string]string, names map[string]bool) (int, error) {
	parts := map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		m, err := catalog.read(byName[name])
		if err != nil {
			return 0, fmt.Errorf("%s", invalidManifestProblem(byName[name], err))
		}
		for _, id := range m.parts {
			parts[id] = true
		}
	}
	rules := 0
	for id := range parts {
		raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
		if err != nil {
			return 0, fmt.Errorf("%s: read fragment: %w", id, err)
		}
		body := raw[len(fragmentHeader(raw)):]
		if fragmentIsRuleByFields(raw, body, fragmentHeaderValues(raw)) {
			rules++
		}
	}
	return rules, nil
}

// IsRoleBudgetNote reports the internal marker used to keep the summary conditional on roles.tsv.
func IsRoleBudgetNote(warning string) bool { return warning == roleBudgetEnabledNote }

func parseRoleSpecs(path string, data []byte) ([]roleSpec, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	line := 0
	var specs []roleSpec
	for scanner.Scan() {
		line++
		text := strings.TrimSuffix(scanner.Text(), "\r")
		if line == 1 {
			if text != "роль\tпредел\tманифесты" {
				return nil, fmt.Errorf("%s:%d: invalid roles.tsv header", path, line)
			}
			continue
		}
		if text == "" {
			return nil, fmt.Errorf("%s:%d: empty roles.tsv row", path, line)
		}
		fields := strings.Split(text, "\t")
		if len(fields) != 3 || strings.TrimSpace(fields[0]) == "" || strings.TrimSpace(fields[2]) == "" {
			return nil, fmt.Errorf("%s:%d: expected role, limit, and manifests separated by tabs", path, line)
		}
		limit, err := strconv.Atoi(strings.TrimSpace(fields[1]))
		if err != nil || limit < 0 {
			return nil, fmt.Errorf("%s:%d: invalid role limit", path, line)
		}
		spec := roleSpec{role: strings.TrimSpace(fields[0]), limit: limit}
		for token := range strings.FieldsSeq(fields[2]) {
			if strings.ContainsAny(token, "{}") {
				if token != "{project}" && token != "{project-rules}" {
					return nil, fmt.Errorf("%s:%d: unknown manifest token %q", path, line, token)
				}
			}
			spec.manifests = append(spec.manifests, token)
		}
		specs = append(specs, spec)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%s: read roles.tsv: %w", path, err)
	}
	if line == 0 {
		return nil, fmt.Errorf("%s: empty roles.tsv", path)
	}
	return specs, nil
}
