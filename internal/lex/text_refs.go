package lex

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type textRefFinding struct {
	kind    int
	path    string
	line    int
	text    string
	missing string
}

func (f textRefFinding) String() string {
	return fmt.Sprintf("%s:%d: kind %d: %s — %s", f.path, f.line, f.kind, strings.TrimSpace(f.text), f.missing)
}

var (
	decisionRef              = regexp.MustCompile("(?i)decisions(?:\\.md)?[`»]?[ ,:]+[`«]?([a-z0-9]+(?:-[a-z0-9]+)+)")
	decisionNext             = regexp.MustCompile("^`?\\s*,\\s*`?([a-z0-9]+(?:-[a-z0-9]+)+)")
	backSlug                 = regexp.MustCompile("`([a-z0-9]+(?:-[a-z0-9]+)+)`")
	backSlugContext          = regexp.MustCompile(`(?i)(?:по|решени[а-я]*|запис[а-я]*|снят[а-я]*)\s*:?\s*$`)
	backSlugNoCeilingContext = regexp.MustCompile(`(?i)не потолок\s*:?\s*$`)
	backSlugDismissedContext = regexp.MustCompile(`(?i)(?:отклон[а-я]*|снят[а-я]*|не действует)\s*\($`)
	relativeSectionRef       = regexp.MustCompile(`(?i)раздел\s+(выше|ниже)`)
	relativeAsideRef         = regexp.MustCompile(`(?i)(?:,\s*(выше|ниже)(?:\s*[),.;:]|$)|подробнее\s+(выше|ниже))`)
	relativeNamedBelowRef    = regexp.MustCompile(`(?i)([^,\n]+),\s*ниже(?:\s*[),.;:]|$)`)
	markdownBullet           = regexp.MustCompile(`^\s*[-*+]\s+`)
	decisionHead             = regexp.MustCompile(`(?m)^##\s+([a-z0-9]+(?:-[a-z0-9]+)+)\s*$`)
	quotedSection            = regexp.MustCompile(`(?i)(?:раздел[а-я]*|пункт[а-я]*|правил(?:о|у|е|ом)|подраздел[а-я]*)\s+«([^«»]+)»`)
	historicalRuleRemoval    = regexp.MustCompile(`(?i)снима[а-я]*\s+.{0,180}правил[а-я]*\s+«[^«»]+»\s*$`)
	historicalRuleStatus     = regexp.MustCompile(`(?i)^\s*(?:снят[а-я]*|отмен[а-я]*|упраздн[а-я]*)`)
	skillSection             = regexp.MustCompile("`([a-z0-9][a-z0-9-]*)`\\s*,\\s*«([^«»]+)»")
	registerSection          = regexp.MustCompile(`(?i)регистр\s*(?:из\s+раздела\s+)?[—-]?\s*«([^«»]+)»`)
	fileRef                  = regexp.MustCompile("`([^`\\n]+\\.md)`")
	markdownRef              = regexp.MustCompile(`\[[^\]]+\]\(([^)]+\.md)(?:#([^)]+))?\)`)
	listItem                 = regexp.MustCompile(`^(\s*)([0-9]{1,6})([a-zа-я]?)[.)]\s+`)
	numberedHeading          = regexp.MustCompile(`^#{2,6}\s+([0-9]{1,6})([a-zа-я]?)\.\s+`)
	stepRef                  = regexp.MustCompile(`(?i)шаг[а-я]*\s+([0-9]+[a-z]?(?:\s*[–—-]\s*[0-9]+[a-z]?)?(?:\s+и\s+[0-9]+[a-z]?)*)`)
	stepNumber               = regexp.MustCompile(`([0-9]+[a-z]?)(?:\s*[–—-]\s*([0-9]+[a-z]?))?`)
	skillNameRef             = regexp.MustCompile("(?i)скилл[а-я]*\\s+`([a-z0-9-]+)`")
	nearbyRef                = regexp.MustCompile("`([^`\\n]+\\.md)`\\s+рядом")
	backDecision             = regexp.MustCompile("`([a-z0-9]+(?:-[a-z0-9]+)+)`\\s+в\\s+`[^`]*decisions\\.md`")
)

type textRefException struct {
	Path     string `json:"path"`
	Kind     int    `json:"kind"`
	Contains string `json:"contains"`
	Reason   string `json:"reason"`
}

type textRefSettings struct {
	TextRefs struct {
		Decisions  string             `json:"decisions"`
		Exceptions []textRefException `json:"exceptions"`
	} `json:"text_refs"`
}

type textCorpus map[string][]byte

func checkTextRefs(codex string, options CheckOptions) ([]string, error) {
	corpus, old, journal, exceptions, skills, err := loadTextCorpus(codex, options)
	if err != nil {
		return nil, err
	}
	findings, unused := applyTextRefExceptions(corpus, findTextRefsWithSkills(corpus, journal, skills), exceptions, filepath.Dir(codex))
	baseline := map[textRefKey]bool{}
	if old != nil {
		oldFindings, _ := applyTextRefExceptions(old, findTextRefsWithSkills(old, journal, skills), exceptions, filepath.Dir(codex))
		for _, f := range oldFindings {
			baseline[refKey(f)] = true
		}
	}
	warnings := append([]string(nil), unused...)
	var problems []string
	for _, f := range findings {
		if baseline[refKey(f)] {
			continue
		}
		if f.kind == 1 {
			problems = append(problems, f.String())
		} else {
			warnings = append(warnings, f.String())
		}
	}
	return finishCheck(warnings, problems)
}

type textRefKey struct {
	kind                int
	path, text, missing string
}

func refKey(f textRefFinding) textRefKey {
	return textRefKey{f.kind, f.path, strings.TrimSpace(f.text), f.missing}
}

type corpusReader struct {
	staged       *stagedPaths
	current, old textCorpus
	// skills names every skill built by the codex, including outputs outside --outputs-under.
	skills map[string]bool
}

func (r *corpusReader) textRefSettings(codex string) (textRefSettings, error) {
	path := filepath.Join(codex, "tools.json")
	data, err := r.read(path)
	if errors.Is(err, os.ErrNotExist) {
		return textRefSettings{}, nil
	}
	if err != nil {
		return textRefSettings{}, fmt.Errorf("read %s: %w", path, err)
	}
	var settings textRefSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return textRefSettings{}, fmt.Errorf("parse %s: %w", path, err)
	}
	seen := map[string]bool{}
	for _, exception := range settings.TextRefs.Exceptions {
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(exception.Path)))
		if exception.Path == "" || filepath.IsAbs(exception.Path) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != exception.Path {
			return textRefSettings{}, fmt.Errorf("%s: text_refs exception path must be relative: %q", path, exception.Path)
		}
		if exception.Kind < 1 || exception.Kind > 4 || exception.Contains == "" || exception.Reason == "" {
			return textRefSettings{}, fmt.Errorf("%s: text_refs exception needs kind 1–4, contains and reason: %s", path, exceptionKey(exception))
		}
		key := exceptionKey(exception)
		if seen[key] {
			return textRefSettings{}, fmt.Errorf("%s: duplicate text_refs exception: %s", path, key)
		}
		seen[key] = true
	}
	return settings, nil
}

func exceptionKey(exception textRefException) string {
	return fmt.Sprintf("%s kind %d contains %q", filepath.ToSlash(exception.Path), exception.Kind, exception.Contains)
}

func applyTextRefExceptions(corpus textCorpus, findings []textRefFinding, exceptions []textRefException, outputRoot string) ([]textRefFinding, []string) {
	used := make([]bool, len(exceptions))
	valid := make([]bool, len(exceptions))
	for i, exception := range exceptions {
		path := filepath.Join(outputRoot, filepath.FromSlash(exception.Path))
		if data, ok := corpus[path]; ok && strings.Count(string(data), exception.Contains) == 1 {
			valid[i] = true
		}
	}
	filtered := make([]textRefFinding, 0, len(findings))
	for _, finding := range findings {
		rel, err := filepath.Rel(outputRoot, finding.path)
		matched := false
		if err == nil {
			path := filepath.ToSlash(rel)
			for i, exception := range exceptions {
				if valid[i] && exception.Kind == finding.kind && exception.Path == path && strings.Contains(finding.text, exception.Contains) {
					used[i] = true
					matched = true
					break
				}
			}
		}
		if !matched {
			filtered = append(filtered, finding)
		}
	}
	var unused []string
	for i, exception := range exceptions {
		if !used[i] {
			unused = append(unused, "unused text reference exception: "+exceptionKey(exception)+" — "+exception.Reason)
		}
	}
	return filtered, unused
}

func (r *corpusReader) read(path string) ([]byte, error) {
	if r.staged == nil {
		return os.ReadFile(path)
	}
	rel := r.staged.rel(path)
	if rel != "" {
		if _, ok := r.staged.index[rel]; !ok {
			return nil, os.ErrNotExist
		}
	}
	return r.staged.read(path)
}

func (r *corpusReader) add(path string) error {
	data, err := r.read(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err == nil {
		r.current[path] = data
	}
	if r.staged == nil {
		return nil
	}
	rel := r.staged.rel(path)
	if rel == "" {
		if err == nil {
			r.old[path] = data
		}
		return nil
	}
	cmd := exec.Command("git", "-C", r.staged.root, "show", "HEAD:"+rel)
	data, err = cmd.Output()
	if err == nil {
		r.old[path] = data
		return nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return nil
	}
	return fmt.Errorf("read HEAD %s: %w", path, err)
}

func outputForRefs(m manifest, o CheckOptions) (string, error) {
	out, err := filepath.Abs(m.out)
	if err != nil {
		return "", fmt.Errorf("resolve output: %w", err)
	}
	if o.OutputsUnder == "" {
		return out, nil
	}
	under, err := filepath.Abs(o.OutputsUnder)
	if err != nil {
		return "", fmt.Errorf("resolve output scope: %w", err)
	}
	rel, err := filepath.Rel(under, out)
	if err != nil {
		return "", fmt.Errorf("relative output %s: %w", out, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", nil
	}
	if o.Tree != "" {
		return filepath.Join(o.Tree, rel), nil
	}
	return out, nil
}

func (r *corpusReader) addManifest(path string, o CheckOptions) error {
	raw, err := r.read(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read manifest %s: %w", path, err)
	}
	m, err := parseManifest(path, raw)
	if err == nil {
		r.noteSkill(m.out)
	}
	out, scopeErr := outputForRefs(m, o)
	if scopeErr != nil {
		return scopeErr
	}
	if out == "" && o.OutputsUnder != "" {
		return nil
	}
	if err != nil {
		return fmt.Errorf("parse manifest %s: %w", path, err)
	}
	if out != "" {
		return r.add(out)
	}
	return nil
}

func (r *corpusReader) noteSkill(out string) {
	dir, file := filepath.Split(filepath.ToSlash(filepath.Clean(out)))
	parent, name := filepath.Split(strings.TrimSuffix(dir, "/"))
	if file != "SKILL.md" || name == "" || filepath.Base(strings.TrimSuffix(parent, "/")) != "skills" {
		return
	}
	if r.skills == nil {
		r.skills = map[string]bool{}
	}
	r.skills[name] = true
}

func (r *corpusReader) decisionPath(codex, override string, settings textRefSettings) (string, error) {
	journal := override
	if journal == "" {
		journal = settings.TextRefs.Decisions
	}
	if journal == "" {
		journal = filepath.Join(filepath.Dir(codex), "decisions.md")
	}
	if !filepath.IsAbs(journal) {
		journal = filepath.Join(codex, journal)
	}
	return filepath.Clean(journal), nil
}

func loadTextCorpus(codex string, o CheckOptions) (textCorpus, textCorpus, string, []textRefException, map[string]bool, error) {
	paths, err := filepath.Glob(filepath.Join(codex, "manifest", "*.yaml"))
	if err != nil {
		return nil, nil, "", nil, nil, fmt.Errorf("list manifests: %w", err)
	}
	r := corpusReader{current: textCorpus{}}
	if o.Staged {
		r.staged, err = readStagedPaths()
		if err != nil {
			return nil, nil, "", nil, nil, err
		}
		r.old = textCorpus{}
		paths = mergePaths(paths, r.staged.indexedUnder(filepath.Join(codex, "manifest"), ".yaml"))
	}
	for _, path := range paths {
		if err := r.addManifest(path, o); err != nil {
			return nil, nil, "", nil, nil, err
		}
	}
	if len(r.current) == 0 && o.OutputsUnder != "" {
		return r.current, r.old, "", nil, r.skills, nil
	}
	settings, err := r.textRefSettings(codex)
	if err != nil {
		return nil, nil, "", nil, nil, err
	}
	journal, err := r.decisionPath(codex, o.Decisions, settings)
	if err != nil {
		return nil, nil, "", nil, nil, err
	}
	if o.Tree != "" {
		mapped, err := outputForRefs(manifest{out: journal}, o)
		if err != nil {
			return nil, nil, "", nil, nil, err
		}
		if mapped != "" {
			journal = mapped
		}
	}
	if _, ok := r.current[journal]; !ok {
		if err := r.add(journal); err != nil {
			return nil, nil, "", nil, nil, err
		}
	}
	if r.staged != nil {
		if _, present := r.current[journal]; !present {
			if _, wasPresent := r.old[journal]; wasPresent {
				r.current[journal] = nil
			}
		}
	}
	return r.current, r.old, journal, settings.TextRefs.Exceptions, r.skills, nil
}

type refScanner struct {
	corpus         textCorpus
	slugs          map[string]bool
	skills         map[string]bool
	hasJournal     bool
	path           string
	line           string
	number         int
	found          []textRefFinding
	last, headings map[int]int
	steps          map[string]bool
}

func (s *refScanner) add(kind int, missing string) {
	s.found = append(s.found, textRefFinding{kind, s.path, s.number, s.line, missing})
}

func (s *refScanner) decisions() {
	if !s.hasJournal {
		return
	}
	for _, match := range decisionRef.FindAllStringSubmatchIndex(s.line, -1) {
		s.decisionSlug(s.line[match[2]:match[3]])
		rest := s.line[match[1]:]
		for {
			next := decisionNext.FindStringSubmatch(rest)
			if next == nil {
				break
			}
			s.decisionSlug(next[1])
			rest = rest[len(next[0]):]
		}
	}
	for _, match := range backDecision.FindAllStringSubmatch(s.line, -1) {
		s.decisionSlug(match[1])
	}
	for _, match := range backSlug.FindAllStringSubmatch(s.line, -1) {
		start := strings.Index(s.line, "`"+match[1]+"`")
		prefix := strings.TrimSpace(s.line[:start])
		context := backSlugContext.MatchString(prefix)
		parenthetical := filepath.Base(s.path) == "decisions.md" && slugInParentheses(s.line, start, start+len(match[0]))
		knownSkill := s.skills[match[1]]
		for path := range s.corpus {
			if strings.HasSuffix(filepath.ToSlash(path), "/skills/"+match[1]+"/SKILL.md") {
				knownSkill = true
				break
			}
		}
		if s.slugs[match[1]] {
			s.decisionSlug(match[1])
		} else if context && !knownSkill || backSlugNoCeilingContext.MatchString(prefix) {
			s.decisionSlug(match[1])
		} else if parenthetical && !knownSkill && (strings.HasSuffix(prefix, "(") || backSlugDismissedContext.MatchString(prefix) || insideQuote(s.line, start)) {
			s.add(2, "неоднозначный slug в скобках: возможная запись "+match[1])
		}
	}
}

func slugInParentheses(line string, start, end int) bool {
	depth, opening := 0, -1
	for i := start - 1; i >= 0; i-- {
		if line[i] == ')' {
			depth++
		} else if line[i] == '(' {
			if depth == 0 {
				opening = i
				break
			}
			depth--
		}
	}
	if opening < 0 {
		return false
	}
	depth = 0
	for i := end; i < len(line); i++ {
		if line[i] == '(' {
			depth++
		} else if line[i] == ')' {
			if depth == 0 {
				return true
			}
			depth--
		}
	}
	return false
}

func insideQuote(line string, index int) bool {
	prefix := line[:index]
	return strings.Count(prefix, "«") > strings.Count(prefix, "»")
}

func (s *refScanner) decisionSlug(slug string) {
	if !s.slugs[slug] {
		s.add(1, "нет записи "+slug)
	}
}

func (s *refScanner) sections() {
	s.relativeSection()
	s.skillSections()
	s.quotedSections()
	s.registerSections()
}

func (s *refScanner) relativeSection() {
	if named := relativeNamedBelowRef.FindStringSubmatch(s.line); named != nil {
		words := strings.Fields(strings.TrimSpace(named[1]))
		if len(words) > 0 {
			name := strings.Trim(words[len(words)-1], "`*_.,:;()[]{}«»\"'")
			if name != "" && !bulletBelowContains(s.corpus[s.path], s.number, name) {
				s.add(2, "нет пункта ниже со словом «"+name+"» в "+s.path)
			}
		}
		return
	}
	relative := relativeSectionRef.FindStringSubmatch(s.line)
	if relative == nil {
		relative = relativeAsideRef.FindStringSubmatch(s.line)
		if relative != nil && relative[1] == "" {
			relative[1] = relative[2]
		}
	}
	if relative != nil {
		lines := strings.Split(string(s.corpus[s.path]), "\n")
		found := false
		if relative[1] == "ниже" {
			for i := s.number; i < len(lines); i++ {
				if strings.HasPrefix(strings.TrimSpace(lines[i]), "#") {
					found = true
					break
				}
			}
		} else {
			for i := s.number - 2; i >= 0 && i < len(lines); i-- {
				if strings.HasPrefix(strings.TrimSpace(lines[i]), "#") {
					found = true
					break
				}
			}
		}
		if !found {
			s.add(2, "нет раздела "+relative[1]+" в "+s.path)
		}
	}
}

func bulletBelowContains(body []byte, lineNumber int, word string) bool {
	i := 0
	for line := range strings.SplitSeq(string(body), "\n") {
		if i >= lineNumber && markdownBullet.MatchString(line) && strings.Contains(strings.ToLower(line), strings.ToLower(word)) {
			return true
		}
		i++
	}
	return false
}

func (s *refScanner) skillSections() {
	for _, match := range skillSection.FindAllStringSubmatch(s.line, -1) {
		name := strings.TrimSuffix(match[1], "/")
		target := filepath.Join(filepath.Dir(filepath.Dir(s.path)), name, "SKILL.md")
		if body, ok := s.corpus[target]; ok && !sectionExists(string(body), match[2], s.line) {
			s.add(2, "нет раздела или пункта «"+match[2]+"» в "+target)
		}
	}
}

func (s *refScanner) quotedSections() {
	for _, indexes := range quotedSection.FindAllStringSubmatchIndex(s.line, -1) {
		match := s.line[indexes[0]:indexes[1]]
		prefix := strings.TrimSpace(s.line[:indexes[0]])
		if strings.HasSuffix(strings.ToLower(prefix), "регистр из") {
			continue
		}
		if historicalRuleRemoval.MatchString(prefix + " " + match) {
			continue
		}
		title := s.line[indexes[2]:indexes[3]]
		if historicalRuleStatus.MatchString(s.line[indexes[1]:]) {
			continue
		}
		target := s.path
		if ref := nearestFileRef(s.line, indexes[2]); ref != "" {
			target = filepath.Join(filepath.Dir(s.path), ref)
		}
		if body, ok := s.corpus[target]; ok && !sectionExists(string(body), title, s.line) {
			if !sectionExistsInSiblingFiles(s.corpus, filepath.Dir(s.path), target, title) {
				s.add(2, "нет раздела или пункта «"+title+"» в "+target)
			}
		}
	}
}

func nearestFileRef(line string, position int) string {
	closest, distance := "", len(line)+1
	for _, indexes := range fileRef.FindAllStringSubmatchIndex(line, -1) {
		start, end := indexes[2], indexes[3]
		d := start - position
		if d < 0 {
			d = -d
		}
		if d < distance {
			closest, distance = line[start:end], d
		}
	}
	return closest
}

func sectionExistsInSiblingFiles(corpus textCorpus, dir, except, title string) bool {
	for path, body := range corpus {
		if filepath.Dir(path) == dir && path != except && sectionExists(string(body), title, "") {
			return true
		}
	}
	return false
}

func (s *refScanner) registerSections() {
	for _, match := range registerSection.FindAllStringSubmatch(s.line, -1) {
		refs := fileRef.FindAllStringSubmatch(s.line, -1)
		target := s.path
		if strings.Contains(strings.ToLower(match[0]), "из раздела") {
			target = filepath.Join(filepath.Dir(s.path), "SKILL.md")
		} else if len(refs) > 0 {
			target = filepath.Join(filepath.Dir(s.path), refs[0][1])
		}
		if body, ok := s.corpus[target]; ok {
			text := strings.ToLower(strings.ReplaceAll(string(body), "ё", "е"))
			needle := strings.ToLower(strings.ReplaceAll(match[1], "ё", "е"))
			if target == s.path {
				text = strings.Replace(text, strings.ToLower(strings.ReplaceAll(s.line, "ё", "е")), "", 1)
			}
			if !strings.Contains(text, needle) {
				s.add(2, "нет регистра «"+match[1]+"» в "+target)
			}
		}
	}
}

func (s *refScanner) missingFile(target string) bool {
	if _, ok := s.corpus[target]; ok {
		return false
	}
	_, err := os.Stat(target)
	return errors.Is(err, os.ErrNotExist)
}

func (s *refScanner) files() {
	for _, match := range markdownRef.FindAllStringSubmatch(s.line, -1) {
		if strings.Contains(match[1], "://") {
			continue
		}
		target := filepath.Join(filepath.Dir(s.path), match[1])
		if s.missingFile(target) {
			s.add(3, "нет файла "+target)
			continue
		}
		body, ok := s.corpus[target]
		if ok && match[2] != "" && !sectionExists(string(body), strings.ReplaceAll(match[2], "-", " "), "") {
			s.add(2, "нет заголовка "+match[2]+" в "+target)
		}
	}
	for _, match := range nearbyRef.FindAllStringSubmatch(s.line, -1) {
		target := filepath.Join(filepath.Dir(s.path), match[1])
		if s.missingFile(target) {
			s.add(3, "нет файла "+target)
		}
	}
}

func dropDeeper(numbers map[int]int, level int) {
	for k := range numbers {
		if k > level {
			delete(numbers, k)
		}
	}
}

func (s *refScanner) heading(trim string) {
	if !strings.HasPrefix(trim, "#") {
		return
	}
	level := len(trim) - len(strings.TrimLeft(trim, "#"))
	dropDeeper(s.headings, level)
	match := numberedHeading.FindStringSubmatch(trim)
	if match == nil {
		delete(s.headings, level)
		return
	}
	n, err := strconv.Atoi(match[1])
	if err != nil {
		s.add(4, fmt.Sprintf("неверный номер раздела %s: %v", match[1], err))
		return
	}
	if match[2] != "" && n == s.headings[level] {
		return
	}
	expected := s.headings[level] + 1
	if n != expected {
		s.add(4, fmt.Sprintf("ожидался раздел %d, найден %d", expected, n))
	}
	s.headings[level] = n
}

func (s *refScanner) list(trim string) {
	match := listItem.FindStringSubmatch(s.line)
	if match == nil {
		if trim != "" && !strings.HasPrefix(s.line, " ") {
			clear(s.last)
		}
		return
	}
	if filepath.Base(s.path) != "SKILL.md" {
		if sibling, ok := s.corpus[filepath.Join(filepath.Dir(s.path), "SKILL.md")]; ok {
			for line := range strings.SplitSeq(string(sibling), "\n") {
				if line == s.line {
					return
				}
			}
		}
	}
	indent := len(match[1])
	n, err := strconv.Atoi(match[2])
	if err != nil {
		s.add(4, fmt.Sprintf("неверный номер пункта %s: %v", match[2], err))
		return
	}
	if match[3] != "" && n == s.last[indent] {
		return
	}
	expected := s.last[indent] + 1
	if !(expected == 1 && n == 0) && n != expected {
		s.add(4, fmt.Sprintf("ожидался пункт %d, найден %d", expected, n))
	}
	s.last[indent] = n
	dropDeeper(s.last, indent)
}

func (s *refScanner) numbering() {
	if hasGeneratedHeader(s.corpus[s.path]) {
		return
	}
	for _, match := range stepRef.FindAllStringSubmatch(s.line, -1) {
		for _, number := range stepNumber.FindAllStringSubmatch(match[1], -1) {
			if strings.IndexFunc(number[1], func(r rune) bool { return r >= 'a' && r <= 'z' }) >= 0 {
				if !s.stepAvailable(number[1]) {
					s.add(4, "нет шага "+number[1])
				}
				continue
			}
			first, err := strconv.Atoi(number[1])
			if err != nil {
				s.add(4, fmt.Sprintf("неверный номер шага %s: %v", number[1], err))
				continue
			}
			last := first
			if number[2] != "" {
				last, err = strconv.Atoi(number[2])
				if err != nil {
					s.add(4, fmt.Sprintf("неверный номер шага %s: %v", number[2], err))
					continue
				}
			}
			for n := first; n <= last; n++ {
				step := strconv.Itoa(n)
				if !s.stepAvailable(step) {
					s.add(4, "нет шага "+step)
				}
			}
		}
	}
	trim := strings.TrimSpace(s.line)
	s.heading(trim)
	s.list(trim)
}

func (s *refScanner) stepAvailable(step string) bool {
	if match := skillNameRef.FindStringSubmatch(s.line); match != nil {
		target := filepath.Join(filepath.Dir(filepath.Dir(s.path)), match[1], "SKILL.md")
		body, ok := s.corpus[target]
		if !ok {
			return true
		}
		return stepExistsInBody(string(body), step)
	}
	if s.steps[step] {
		return true
	}
	if strings.IndexFunc(step, func(r rune) bool { return r >= 'a' && r <= 'z' }) >= 0 {
		return stepExistsAnywhere(s.corpus, step)
	}
	return false
}

func stepExistsAnywhere(corpus textCorpus, step string) bool {
	for _, body := range corpus {
		if stepExistsInBody(string(body), step) {
			return true
		}
	}
	return false
}

func stepExistsInBody(body, step string) bool {
	for line := range strings.SplitSeq(body, "\n") {
		if item := listItem.FindStringSubmatch(line); item != nil && item[2]+item[3] == step {
			return true
		}
		if heading := numberedHeading.FindStringSubmatch(strings.TrimSpace(line)); heading != nil && heading[1]+heading[2] == step {
			return true
		}
	}
	return false
}

func (s *refScanner) scan(path string) {
	s.path = path
	s.last, s.headings, s.steps = map[int]int{}, map[int]int{}, map[string]bool{}
	lines := strings.Split(string(s.corpus[path]), "\n")
	stepLines := lines
	if sibling, ok := s.corpus[filepath.Join(filepath.Dir(path), "SKILL.md")]; ok && filepath.Base(path) != "SKILL.md" {
		stepLines = append(append([]string{}, lines...), strings.Split(string(sibling), "\n")...)
	}
	for _, line := range stepLines {
		if m := listItem.FindStringSubmatch(line); m != nil {
			s.steps[m[2]+m[3]] = true
		}
		if m := numberedHeading.FindStringSubmatch(line); m != nil {
			s.steps[m[1]+m[2]] = true
		}
	}
	fence := false
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			fence = !fence
			continue
		}
		if fence {
			continue
		}
		s.number, s.line = i+1, line
		s.decisions()
		s.sections()
		s.files()
		s.numbering()
	}
}

func findTextRefs(corpus textCorpus, journal string) []textRefFinding {
	return findTextRefsWithSkills(corpus, journal, nil)
}

// findTextRefsWithSkills also treats names in skills as known skills: with
// --outputs-under the corpus holds one project, not the codex's skills.
func findTextRefsWithSkills(corpus textCorpus, journal string, skills map[string]bool) []textRefFinding {
	s := refScanner{corpus: corpus, slugs: map[string]bool{}, skills: skills}
	journalData, hasJournal := corpus[journal]
	s.hasJournal = hasJournal
	for _, m := range decisionHead.FindAllStringSubmatch(string(journalData), -1) {
		s.slugs[m[1]] = true
	}
	for _, path := range slices.Sorted(maps.Keys(corpus)) {
		s.scan(path)
	}
	return s.found
}

func sectionExists(body, title, referenceLine string) bool {
	norm := func(s string) string { return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "ё", "е")) }
	title = norm(title)
	for line := range strings.SplitSeq(body, "\n") {
		if line == referenceLine {
			continue
		}
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "-") || listItem.MatchString(t) {
			if strings.Contains(norm(t), title) {
				return true
			}
		}
	}
	return false
}
