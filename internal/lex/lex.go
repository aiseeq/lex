package lex

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

type outputLink struct {
	path, target string
	targetSet    bool
}

type manifestError struct {
	path string
	err  error
}

func (e *manifestError) Error() string { return e.path + ": " + e.err.Error() }
func (e *manifestError) Unwrap() error { return e.err }

type manifest struct {
	out, hash      string
	parts          []string
	links          []outputLink
	fragmentHashes map[string]string
	builtHash      string
	header         bool
}

const (
	directoryMode = 0755
	fileMode      = 0644
)

func snapshotCodexFiles(codex string) (map[string][]byte, error) {
	files := make(map[string][]byte)
	err := filepath.WalkDir(codex, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = body
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%s: snapshot codex files: %w", codex, err)
	}
	return files, nil
}

func changedCodexFiles(before, after map[string][]byte) []string {
	seen := make(map[string]struct{}, len(before)+len(after))
	for path := range before {
		seen[path] = struct{}{}
	}
	for path := range after {
		seen[path] = struct{}{}
	}
	var changed []string
	for path := range seen {
		old, existed := before[path]
		current, exists := after[path]
		if existed != exists || existed && !bytes.Equal(old, current) {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	return changed
}

func codexKey(rel string) string {
	var b strings.Builder
	for _, r := range rel {
		switch {
		case r == '/':
			b.WriteByte('-')
		case r == '-':
			b.WriteString("%2D")
		case r == '%':
			b.WriteString("%25")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func validateRefresh(codex, topic string, m manifest) error {
	listed := make(map[string]bool, len(m.parts))
	problems := []string{}
	for _, id := range m.parts {
		path := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
		listed[filepath.Clean(path)] = true
		b, err := os.ReadFile(path)
		if err != nil {
			problems = append(problems, path+": "+err.Error())
			continue
		}
		if expected, ok := m.fragmentHashes[id]; !ok || sha(b) != expected {
			problems = append(problems, path+": fragment body was edited")
		}
	}
	root := filepath.Join(codex, "frag", filepath.FromSlash(topic))
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		if !listed[filepath.Clean(path)] {
			b, err := os.ReadFile(path)
			if err == nil && fragmentIsRecord(b) {
				return nil
			}
			problems = append(problems, path+": fragment is not listed in manifest")
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		problems = append(problems, root+": "+err.Error())
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return errors.New(strings.Join(problems, "\n"))
}

type refreshAssessment struct {
	sourceData     []byte
	cleanData      []byte
	purposeEnabled bool
	noChange       bool
}

func assessRefresh(codex, topic, manifestPath string, m manifest, diskData []byte) (refreshAssessment, error) {
	sourceData := diskData
	if m.header {
		sourceData = withoutGeneratedHeader(diskData, manifestPath)
	}
	validationErr := validateRefresh(codex, topic, m)
	purposeEnabled, cleanData, err := prepareRefreshPurpose(codex, m, sourceData)
	if err != nil {
		return refreshAssessment{}, err
	}
	assessment := refreshAssessment{
		sourceData:     sourceData,
		cleanData:      cleanData,
		purposeEnabled: purposeEnabled,
		noChange:       sha(sourceData) == m.hash,
	}
	if !assessment.noChange && validationErr != nil {
		return assessment, validationErr
	}
	return assessment, nil
}

type ImportOptions struct {
	Refresh bool
	Home    string
}

func Import(codex, source string, refresh bool) error {
	return ImportWithOptions(codex, source, ImportOptions{Refresh: refresh})
}
func ImportWithOptions(codex, source string, options ImportOptions) error {
	codex, err := canonicalCodex(codex, true)
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(source)
	if err != nil {
		return fmt.Errorf("%s: resolve source path: %w", source, err)
	}
	linkPath := abs
	linkTarget, linkErr := os.Readlink(linkPath)
	isLink := linkErr == nil
	real, evalErr := filepath.EvalSymlinks(abs)
	if isLink {
		immediate := linkTarget
		if !filepath.IsAbs(immediate) {
			immediate = filepath.Join(filepath.Dir(linkPath), immediate)
		}
		info, statErr := os.Lstat(immediate)
		if statErr != nil {
			return fmt.Errorf("%s: symlink target %s: %w", linkPath, immediate, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s: symlink chain through %s is not supported", linkPath, immediate)
		}
		linkPath = filepath.Join(resolvePath(filepath.Dir(linkPath)), filepath.Base(linkPath))
	}
	if evalErr == nil {
		abs = real
	}
	home := options.Home
	if home == "" {
		current, e := os.UserHomeDir()
		if e != nil {
			return fmt.Errorf("find current user's home: %w", e)
		}
		home = current
	}
	rel, err := filepath.Rel(home, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s: source must be under $HOME", source)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return fmt.Errorf("%s: %w", abs, err)
	}
	if err := rejectForeignOutput(codex, abs, data); err != nil {
		return err
	}
	var links []outputLink
	if isLink {
		links = append(links, outputLink{path: linkPath, target: linkTarget, targetSet: true})
	}
	return importDataWithLinks(codex, abs, rel, data, options.Refresh, links)
}

func importData(codex, abs, rel string, data []byte, refresh bool) error {
	return importDataWithLinks(codex, abs, rel, data, refresh, nil)
}

func importDataWithLinks(codex, abs, rel string, data []byte, refresh bool, links []outputLink) error {
	diskHash := sha(data)
	key := codexKey(filepath.ToSlash(rel))
	topic := key
	manifestPath := filepath.Join(codex, "manifest", key+".yaml")
	var old manifest
	var preservedHeaders []preservedFragmentHeader
	var units [][]byte
	var sourceData []byte
	var assessment refreshAssessment
	old, err := readManifest(manifestPath)
	if err == nil {
		if old.out != abs {
			return fmt.Errorf("%s: manifest key already points to %s", manifestPath, old.out)
		}
		if refresh {
			assessment, err = assessRefresh(codex, topic, manifestPath, old, data)
			if err != nil {
				return err
			}
			data = assessment.sourceData
		}
		mergedLinks := uniqueLinks(append(old.links, links...))
		unchanged := assessment.noChange
		if !refresh {
			unchanged = old.hash == sha(data)
		}
		if unchanged {
			if equalLinks(old.links, mergedLinks) {
				return nil
			}
			old.links = mergedLinks
			return writeManifest(manifestPath, old, mergedLinks)
		}
		if !refresh {
			return fmt.Errorf("%s: source changed; re-import with --refresh", abs)
		}
		preservedHeaders, err = readFragmentHeaders(codex, old.parts)
		if err != nil {
			return err
		}
		sourceData = append([]byte(nil), data...)
		if assessment.purposeEnabled {
			units, err = refreshPurposeUnits(data, preservedHeaders)
			if err != nil {
				return err
			}
		} else {
			units = split(assessment.cleanData)
		}
		if err := removeOldFragments(codex, old.parts, os.Remove); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s: %w", manifestPath, err)
	}
	if units == nil {
		units = split(data)
	}
	parts, hashes, err := writeImportedFragments(codex, topic, units, preservedHeaders)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(manifestPath), directoryMode); err != nil {
		return fmt.Errorf("%s: create manifest directory: %w", manifestPath, err)
	}
	if sourceData == nil {
		sourceData = data
	}
	m := manifest{out: abs, hash: sha(sourceData), builtHash: diskHash, parts: parts, fragmentHashes: hashes, header: old.header}
	allLinks := uniqueLinks(append(old.links, links...))
	m.links = allLinks
	return finishImport(codex, manifestPath, m, allLinks, topic, units, preservedHeaders)
}

func finishImport(codex, manifestPath string, m manifest, links []outputLink, topic string, units [][]byte, preservedHeaders []preservedFragmentHeader) error {
	if err := writeManifest(manifestPath, m, links); err != nil {
		return err
	}
	if len(preservedHeaders) > 0 {
		return rewriteOverridesAfterRefresh(codex, topic, units, preservedHeaders)
	}
	return nil
}

func writeImportedFragments(codex, topic string, units [][]byte, preservedHeaders []preservedFragmentHeader) ([]string, map[string]string, error) {
	parts := make([]string, 0, len(units))
	hashes := make(map[string]string, len(units))
	ids, matched, usedHeaders := matchImportedHeaders(topic, units, preservedHeaders)
	for i, u := range units {
		id := ids[i]
		parts = append(parts, id)
		path := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
		if existing, err := os.ReadFile(path); err == nil && fragmentIsRecord(existing) {
			return nil, nil, fmt.Errorf("%s: imported fragment would overwrite a record", path)
		}
		if err := os.MkdirAll(filepath.Dir(path), directoryMode); err != nil {
			return nil, nil, fmt.Errorf("%s: create fragment directory: %w", path, err)
		}
		header := []byte("---\nid: " + id + "\nцель: \nоснование: \nснять_когда: \n---\n")
		if oldIndex := matched[i]; oldIndex >= 0 {
			header = replaceHeaderID(preservedHeaders[oldIndex].header, id)
		}
		body := append(header, u...)
		if err := os.WriteFile(path, body, fileMode); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", path, err)
		}
		hashes[id] = sha(body)
	}
	for i, oldHeader := range preservedHeaders {
		if !usedHeaders[i] && oldHeader.hasValues {
			if err := saveLostCodeLinks(codex, oldHeader); err != nil {
				return nil, nil, err
			}
			fmt.Fprintf(os.Stderr, "warning: %s: non-empty fragment header was not preserved by refresh\n", oldHeader.id)
		}
	}
	return parts, hashes, nil
}

func matchImportedHeaders(topic string, units [][]byte, preservedHeaders []preservedFragmentHeader) ([]string, []int, []bool) {
	counts := map[string]int{}
	usedHeaders := make([]bool, len(preservedHeaders))
	ids := make([]string, len(units))
	labels := make([]string, len(units))
	oldCounts := map[string]int{}
	for _, old := range preservedHeaders {
		name := old.id[strings.LastIndex(old.id, "/")+1:]
		if end := strings.LastIndex(name, "-"); end >= 0 {
			oldCounts[name[:end]]++
		}
	}
	for i, unit := range units {
		label := slug(unit)
		if label == "" {
			label = "fragment"
		}
		counts[label]++
		labels[i] = label
		ids[i] = fmt.Sprintf("%s/%s-%d", topic, label, counts[label])
	}
	matched := make([]int, len(units))
	for i, unit := range units {
		matched[i] = -1
		for j, old := range preservedHeaders {
			if !usedHeaders[j] && old.bodyHash == sha(unit) {
				matched[i], usedHeaders[j] = j, true
				break
			}
		}
	}
	for i, id := range ids {
		if matched[i] >= 0 || counts[labels[i]] != oldCounts[labels[i]] {
			continue
		}
		for j, old := range preservedHeaders {
			if !usedHeaders[j] && old.id == id {
				matched[i], usedHeaders[j] = j, true
				break
			}
		}
	}
	return ids, matched, usedHeaders
}

type preservedFragmentHeader struct {
	id        string
	header    []byte
	bodyHash  string
	hasValues bool
}

func readFragmentHeaders(codex string, parts []string) ([]preservedFragmentHeader, error) {
	headers := make([]preservedFragmentHeader, 0, len(parts))
	for _, id := range parts {
		path := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s: read fragment header: %w", path, err)
		}
		header := fragmentHeader(raw)
		body, err := fragmentBody(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		headers = append(headers, preservedFragmentHeader{id: id, header: header, bodyHash: sha(body), hasValues: fragmentHeaderHasValues(header)})
	}
	return headers, nil
}

func fragmentHeaderHasValues(header []byte) bool {
	for line := range strings.SplitSeq(string(header), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok && key != "id" && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func writeManifest(manifestPath string, m manifest, links []outputLink) error {
	var content strings.Builder
	content.WriteString("out: " + strconv.Quote(m.out) + "\nsource_sha256: " + m.hash + "\nbuilt_sha256: " + m.builtHash + "\nheader: " + strconv.FormatBool(m.header) + "\n")
	if len(links) > 0 {
		content.WriteString("links:\n")
		for _, link := range uniqueLinks(links) {
			content.WriteString("  - path: " + strconv.Quote(link.path) + "\n    target: " + strconv.Quote(link.target) + "\n")
		}
	}
	content.WriteString("parts:\n")
	for _, id := range m.parts {
		content.WriteString("  - " + strconv.Quote(id) + "\n")
	}
	content.WriteString("fragment_sha256:\n")
	for _, id := range m.parts {
		if hash := m.fragmentHashes[id]; hash != "" {
			content.WriteString("  " + strconv.Quote(id) + ": " + hash + "\n")
		}
	}
	if err := os.WriteFile(manifestPath, []byte(content.String()), fileMode); err != nil {
		return fmt.Errorf("%s: %w", manifestPath, err)
	}
	return nil
}

func equalLinks(a, b []outputLink) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].path != b[i].path || a[i].target != b[i].target {
			return false
		}
	}
	return true
}

func uniqueLinks(links []outputLink) []outputLink {
	byPath := make(map[string]string, len(links))
	var paths []string
	for _, link := range links {
		if _, exists := byPath[link.path]; !exists {
			paths = append(paths, link.path)
		}
		byPath[link.path] = link.target
	}
	sort.Strings(paths)
	out := make([]outputLink, 0, len(paths))
	for _, path := range paths {
		out = append(out, outputLink{path: path, target: byPath[path]})
	}
	return out
}

func removeOldFragments(codex string, parts []string, remove func(string) error) error {
	for _, id := range parts {
		path := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
		if err := remove(path); err != nil {
			return fmt.Errorf("%s: remove fragment: %w", path, err)
		}
	}
	return nil
}

func Build(codex string, check bool) error {
	return BuildWithOptions(codex, BuildOptions{Check: check})
}

type BuildOptions struct {
	Check, Force bool
	Output       string
}
type BuildCheckOptionsValue struct {
	NoHeader bool
	Output   string
}

var errManualOutputEdits = errors.New("output has manual edits; use lex absorb or --force")

func BuildCheckOptions(codex string, options BuildCheckOptionsValue) error {
	codex, err := canonicalCodex(codex, false)
	if err != nil {
		return err
	}
	if !options.NoHeader {
		return BuildWithOptions(codex, BuildOptions{Check: true, Output: options.Output})
	}
	paths, err := filepath.Glob(filepath.Join(codex, "manifest", "*.yaml"))
	if err != nil {
		return fmt.Errorf("list manifests: %w", err)
	}
	sort.Strings(paths)
	paths, err = selectBuildOutput(paths, options.Output)
	if err != nil {
		return err
	}
	var linkProblems []string
	manifests := make([]manifest, 0, len(paths))
	for _, path := range paths {
		m, err := readManifest(path)
		if err != nil {
			return err
		}
		linkProblems = append(linkProblems, diskLinkProblems(m.links)...)
		manifests = append(manifests, m)
	}
	if len(linkProblems) > 0 {
		return errors.New(strings.Join(linkProblems, "\n"))
	}
	for i, path := range paths {
		m := manifests[i]
		want, err := manifestBody(codex, m)
		if err != nil {
			return err
		}
		actual, err := os.ReadFile(m.out)
		if err != nil {
			return fmt.Errorf("%s: %w", m.out, err)
		}
		if m.header {
			actual = withoutGeneratedHeader(actual, path)
		}
		if !bytes.Equal(actual, want) {
			return fmt.Errorf("%s: built output differs", m.out)
		}
	}
	return nil
}
func BuildWithOptions(codex string, options BuildOptions) error {
	_, err := BuildWithOptionsAndPaths(codex, options)
	return err
}

func BuildWithOptionsAndPaths(codex string, options BuildOptions) ([]string, error) {
	codex, err := canonicalCodex(codex, false)
	if err != nil {
		return nil, err
	}
	if options.Check {
		return nil, buildWithCanonicalCodex(codex, options)
	}
	before, err := snapshotCodexFiles(codex)
	if err != nil {
		return nil, err
	}
	err = buildWithCanonicalCodex(codex, options)
	after, snapshotErr := snapshotCodexFiles(codex)
	if snapshotErr != nil {
		return nil, errors.Join(err, snapshotErr)
	}
	return changedCodexFiles(before, after), err
}

func buildWithCanonicalCodex(codex string, options BuildOptions) error {
	paths, err := filepath.Glob(filepath.Join(codex, "manifest", "*.yaml"))
	if err != nil {
		return fmt.Errorf("%s: list manifests: %w", codex, err)
	}
	sort.Strings(paths)
	paths, err = selectBuildOutput(paths, options.Output)
	if err != nil {
		return err
	}
	if !options.Check && !options.Force {
		if err := guardForeignBuildOutputs(codex, paths); err != nil {
			return err
		}
	}
	if err := prepareBuildLinks(paths, options.Check); err != nil {
		return err
	}
	differences, manualEdits, err := buildManifests(codex, paths, options)
	if err != nil {
		return err
	}
	if len(manualEdits) > 0 {
		return errors.New(strings.Join(manualEdits, "\n"))
	}
	if options.Check && len(differences) > 0 {
		differences = append(differences, fmt.Sprintf("%d of %d differ", len(differences), len(paths)))
		return errors.New(strings.Join(differences, "\n"))
	}
	return nil
}

func selectBuildOutput(paths []string, output string) ([]string, error) {
	if output == "" {
		return paths, nil
	}
	target, err := filepath.Abs(output)
	if err != nil {
		return nil, fmt.Errorf("resolve output path: %w", err)
	}
	selected, _, err := findOutputManifest(paths, target)
	if err != nil {
		return nil, err
	}
	return []string{selected}, nil
}

func prepareBuildLinks(paths []string, check bool) error {
	var linkProblems []string
	for _, path := range paths {
		m, err := readManifest(path)
		if err != nil {
			return err
		}
		if check {
			linkProblems = append(linkProblems, diskLinkProblems(m.links)...)
		} else if err := prepareLinks(m.links); err != nil {
			return err
		}
	}
	if len(linkProblems) > 0 {
		return errors.New(strings.Join(linkProblems, "\n"))
	}
	return nil
}

func guardForeignBuildOutputs(codex string, paths []string) error {
	for _, path := range paths {
		m, err := readManifest(path)
		if err != nil {
			return err
		}
		actual, err := os.ReadFile(m.out)
		if err == nil {
			if err := rejectForeignOutput(codex, m.out, actual); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("%s: %w", m.out, err)
		}
	}
	return nil
}

func buildManifests(codex string, paths []string, options BuildOptions) ([]string, []string, error) {
	var differences []string
	var manualEdits []string
	for _, path := range paths {
		difference, err := buildManifest(codex, path, options)
		if err != nil {
			if errors.Is(err, errManualOutputEdits) && options.Output == "" {
				manualEdits = append(manualEdits, err.Error())
				continue
			}
			return nil, nil, err
		}
		if difference != "" {
			differences = append(differences, difference)
		}
	}
	return differences, manualEdits, nil
}

type editWarningSnapshot struct {
	mtime    string
	fileGit  string
	partsGit string
}

// gitLocalEnv lists the variables of `git rev-parse --local-env-vars`: inherited from a hook or another repository, they would make git report the state of the wrong repository.
var gitLocalEnv = map[string]bool{"GIT_ALTERNATE_OBJECT_DIRECTORIES": true, "GIT_CONFIG": true, "GIT_CONFIG_PARAMETERS": true, "GIT_CONFIG_COUNT": true, "GIT_OBJECT_DIRECTORY": true, "GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_IMPLICIT_WORK_TREE": true, "GIT_GRAFT_FILE": true, "GIT_INDEX_FILE": true, "GIT_NO_REPLACE_OBJECTS": true, "GIT_REPLACE_REF_BASE": true, "GIT_PREFIX": true, "GIT_SHALLOW_FILE": true, "GIT_COMMON_DIR": true}

// gitStatusEnv runs git for the warning in the repository around the path, with English messages and without taking the index lock another session may need.
func gitStatusEnv() []string {
	env := []string{"LC_ALL=C", "GIT_OPTIONAL_LOCKS=0"}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !gitLocalEnv[key] && key != "LC_ALL" && key != "GIT_OPTIONAL_LOCKS" {
			env = append(env, entry)
		}
	}
	return env
}

func gitStatus(cwd string, paths ...string) (string, string, error) {
	git, err := exec.LookPath("git")
	if errors.Is(err, exec.ErrNotFound) {
		return "", "", errors.New("git not found")
	}
	if err != nil {
		return "", "", fmt.Errorf("find git: %w", err)
	}
	args := append([]string{"--literal-pathspecs", "-C", cwd, "status", "--porcelain=v1", "--untracked-files=all", "--ignored", "--"}, paths...)
	cmd := exec.Command(git, args...)
	cmd.Env = gitStatusEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err == nil {
		return string(output), "", nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 128 && strings.Contains(strings.ToLower(stderr.String()), "not a git repository") {
		return "", "not in a git repository", nil
	}
	reason := strings.TrimSpace(stderr.String())
	if reason == "" {
		reason = err.Error()
	}
	reason = strings.NewReplacer("\r", " ", "\n", " ").Replace(reason)
	return "", "", errors.New(reason)
}

func fileGitState(path string) string {
	dir, base := filepath.Dir(path), filepath.Base(path)
	output, absentRepo, err := gitStatus(dir, base)
	if absentRepo != "" {
		return absentRepo
	}
	if err != nil {
		return "unknown (" + err.Error() + ")"
	}
	if output == "" {
		return "committed"
	}
	line := strings.TrimRight(output, "\n")
	if strings.HasPrefix(line, "??") {
		return "untracked"
	}
	if strings.HasPrefix(line, "!!") {
		return "ignored"
	}
	x, y := line[0], line[1]
	if x != ' ' && y != ' ' {
		return "staged and modified"
	}
	if x != ' ' {
		return "staged"
	}
	if y != ' ' {
		return "modified"
	}
	return "committed"
}

func fragmentsGitState(codex string, m manifest, manifestPath string) string {
	paths := make([]string, 0, len(m.parts)+1)
	for _, id := range m.parts {
		paths = append(paths, filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
	}
	paths = append(paths, manifestPath)
	output, absentRepo, err := gitStatus(codex, paths...)
	if absentRepo != "" {
		return absentRepo
	}
	if err != nil {
		return "unknown (" + err.Error() + ")"
	}
	if output == "" {
		return "committed"
	}
	if files := strings.Count(strings.TrimSuffix(output, "\n"), "\n") + 1; files > 1 {
		return fmt.Sprintf("uncommitted changes in %d files", files)
	}
	return "uncommitted changes in 1 file"
}

func snapshotEditWarning(output, codex, manifestPath string, m manifest) *editWarningSnapshot {
	info, err := os.Stat(output)
	mtime := ""
	if err != nil {
		mtime = "unknown (" + err.Error() + ")"
	} else {
		mtime = info.ModTime().In(time.Local).Format("2006-01-02 15:04:05 MST")
	}
	return &editWarningSnapshot{
		mtime:    mtime,
		fileGit:  fileGitState(output),
		partsGit: fragmentsGitState(codex, m, manifestPath),
	}
}

func printEditWarning(kind, output string, s *editWarningSnapshot) {
	if kind == "build" {
		fmt.Fprintf(os.Stderr, "warning: %s: --force replaced an edit that was not in the fragments (file changed %s; git: %s; fragments: %s); if you did not make that edit, another session may have been working on this file\n", output, s.mtime, s.fileGit, s.partsGit)
		return
	}
	fmt.Fprintf(os.Stderr, "warning: %s: absorb moved an edit into the fragments (file changed %s; git: %s; fragments: %s); if you did not make that edit, another session may still be working on this file — let it finish before you commit\n", output, s.mtime, s.fileGit, s.partsGit)
}

func buildManifest(codex, path string, options BuildOptions) (string, error) {
	m, err := readManifest(path)
	if err != nil {
		return "", err
	}
	out, err := manifestBody(codex, m)
	if err != nil {
		return "", err
	}
	if m.header {
		out = generatedHeader(path, out)
	}
	if options.Check {
		return compareOutput(m.out, out)
	}
	if info, e := os.Lstat(m.out); e == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s: refusing to write through symlink", m.out)
	} else if e != nil && !os.IsNotExist(e) {
		return "", fmt.Errorf("%s: inspect output: %w", m.out, e)
	}
	actual, e := os.ReadFile(m.out)
	if m.builtHash == "" && !options.Force {
		return "", fmt.Errorf("%s: manifest has no built_sha256; use lex absorb or --force", path)
	}
	var warnSnapshot *editWarningSnapshot
	if options.Force && outputHasManualEdits(e, actual, out, m, path, false) {
		warnSnapshot = snapshotEditWarning(m.out, codex, path, m)
	}
	if outputHasManualEdits(e, actual, out, m, path, options.Force) {
		return "", fmt.Errorf("%s: %w", m.out, errManualOutputEdits)
	}
	if e != nil && !os.IsNotExist(e) {
		return "", fmt.Errorf("%s: %w", m.out, e)
	}
	if e = os.MkdirAll(filepath.Dir(m.out), directoryMode); e != nil {
		return "", fmt.Errorf("%s: create output directory: %w", m.out, e)
	}
	if e = os.WriteFile(m.out, out, fileMode); e != nil {
		return "", fmt.Errorf("%s: %w", m.out, e)
	}
	if e = updateManifestField(path, "built_sha256", sha(out)); e != nil {
		return "", fmt.Errorf("%s: update manifest: %w", path, e)
	}
	if warnSnapshot != nil {
		printEditWarning("build", m.out, warnSnapshot)
	}
	return "", nil
}

func manifestScope(m manifest, under, tree string) (bool, []outputLink, error) {
	out, err := filepath.Abs(m.out)
	if err != nil {
		return false, nil, fmt.Errorf("%s: resolve output path: %w", m.out, err)
	}
	scopePath := resolvePath(out)
	if tree != "" {
		scopePath = out
	}
	outputInScope, err := pathUnder(scopePath, under)
	if err != nil {
		return false, nil, err
	}
	var links []outputLink
	for _, link := range m.links {
		inScope, err := pathUnder(link.path, under)
		if err != nil {
			return false, nil, err
		}
		if inScope {
			links = append(links, link)
		}
	}
	return outputInScope, links, nil
}

func pathUnder(path, under string) (bool, error) {
	if !filepath.IsAbs(path) {
		return false, fmt.Errorf("%s: path must be absolute", path)
	}
	rel, err := filepath.Rel(under, filepath.Clean(path))
	if err != nil {
		return false, fmt.Errorf("compare path %s with %s: %w", path, under, err)
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}

func diskLinkProblems(links []outputLink, inTree ...bool) []string {
	var problems []string
	formatMismatch := linkMismatch
	if len(inTree) > 0 && inTree[0] {
		formatMismatch = linkMismatchInTree
	}
	for _, link := range links {
		info, err := os.Lstat(link.path)
		if os.IsNotExist(err) {
			problems = append(problems, formatMismatch(link, "missing").Error())
			continue
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: inspect symlink: %v", link.path, err))
			continue
		}
		if info.Mode()&os.ModeSymlink == 0 {
			problems = append(problems, formatMismatch(link, "path is occupied by a non-symlink").Error())
			continue
		}
		target, err := os.Readlink(link.path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: read symlink: %v", link.path, err))
		} else if target != link.target {
			problems = append(problems, formatMismatch(link, "points to "+strconv.Quote(target)).Error())
		}
	}
	return problems
}

func prepareLinks(links []outputLink) error {
	for _, link := range links {
		info, err := os.Lstat(link.path)
		if os.IsNotExist(err) {
			if err := os.MkdirAll(filepath.Dir(link.path), directoryMode); err != nil {
				return fmt.Errorf("%s: create symlink directory: %w", link.path, err)
			}
			if err := os.Symlink(link.target, link.path); err != nil {
				return fmt.Errorf("%s: create symlink: %w", link.path, err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("%s: inspect symlink: %w", link.path, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return linkMismatch(link, "path is occupied by a non-symlink")
		}
		target, err := os.Readlink(link.path)
		if err != nil {
			return fmt.Errorf("%s: read symlink: %w", link.path, err)
		}
		if target != link.target {
			return linkMismatch(link, "points to "+strconv.Quote(target))
		}
	}
	return nil
}

func linkMismatch(link outputLink, state string) error {
	return fmt.Errorf("%s: symlink %s; expected target %q; restore with ln -sfn %q %q or run lex build",
		link.path, state, link.target, link.target, link.path)
}

func linkMismatchInTree(link outputLink, state string) error {
	return fmt.Errorf("%s: symlink %s; expected target %q; restore with ln -sfn %q %q",
		link.path, state, link.target, link.target, link.path)
}

func manifestBody(codex string, m manifest) ([]byte, error) {
	return manifestBodyWithRead(codex, m, os.ReadFile)
}

func manifestBodyWithRead(codex string, m manifest, read func(string) ([]byte, error)) ([]byte, error) {
	enabled, err := readPurposeSetting(codex, read)
	if err != nil {
		return nil, err
	}
	var out []byte
	for i, id := range m.parts {
		p := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
		raw, e := read(p)
		if e != nil {
			return nil, fmt.Errorf("%s: %w", p, e)
		}
		if fragmentIsRecord(raw) {
			return nil, fmt.Errorf("%s: record must not be in a manifest", p)
		}
		body, e := fragmentBody(raw)
		if e != nil {
			return nil, fmt.Errorf("%s: %w", p, e)
		}
		if i < len(m.parts)-1 && len(body) > 0 && body[len(body)-1] != '\n' {
			return nil, fmt.Errorf("%s: fragment before last has no final newline", p)
		}
		body, _, e = renderPurpose(p, raw, body, enabled)
		if e != nil {
			return nil, e
		}
		out = append(out, body...)
	}
	return out, nil
}
func compareOutput(path string, want []byte) (string, error) {
	actual, e := os.ReadFile(path)
	if e != nil {
		return "", fmt.Errorf("%s: %w", path, e)
	}
	if bytes.Equal(actual, want) {
		return "", nil
	}
	line, a, b := firstDiff(actual, want)
	return fmt.Sprintf("%s: first differing line %d: source %q, built %q", path, line, trunc(a), trunc(b)), nil
}

func generatedHeader(manifestPath string, body []byte) []byte {
	line := generatedMarker(manifestPath)
	lines := bytes.SplitAfter(body, []byte("\n"))
	count := frontMatterLength(lines)
	if count > 0 {
		return bytes.Join(append(append(append([][]byte{}, lines[:count]...), line), lines[count:]...), nil)
	}
	return append(line, body...)
}
func generatedMarker(manifestPath string) []byte {
	key := strings.TrimSuffix(filepath.Base(manifestPath), ".yaml")
	codex := resolvePath(filepath.Dir(filepath.Dir(manifestPath)))
	return generatedMarkerForCodex(key, displayCodexPath(codex))
}

func generatedMarkerForCodex(key, display string) []byte {
	return []byte("<!-- lex: собран из " + display + "/manifest/" + key + ".yaml. Правь фрагменты в " + display + "/frag и запусти `lex build --codex " + display + "`; правку прямо в этом файле перенеси во фрагменты: `lex absorb --codex " + display + " <этот файл>` -->\n")
}

func displayCodexPath(codex string) string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		if relative, relErr := filepath.Rel(resolvePath(home), resolvePath(codex)); relErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			if relative == "." {
				return "~"
			}
			return "~/" + filepath.ToSlash(relative)
		}
	}
	return resolvePath(codex)
}

func generatedMarkerForms(manifestPath string) [][]byte {
	codex := resolvePath(filepath.Dir(filepath.Dir(manifestPath)))
	key := strings.TrimSuffix(filepath.Base(manifestPath), ".yaml")
	preferred := displayCodexPath(codex)
	forms := [][]byte{generatedMarkerForCodex(key, preferred)}
	absolute := generatedMarkerForCodex(key, codex)
	if !bytes.Equal(forms[0], absolute) {
		forms = append(forms, absolute)
	}
	return forms
}

func hasGeneratedHeader(data []byte) bool {
	lines := bytes.SplitAfter(data, []byte("\n"))
	offset := 0
	for _, line := range lines[:frontMatterLength(lines)] {
		offset += len(line)
	}
	return bytes.HasPrefix(data[offset:], []byte("<!-- lex: собран из "))
}

func expandHeaderCodexPath(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if path == "~" {
				return resolvePath(home)
			}
			return resolvePath(filepath.Join(home, filepath.FromSlash(strings.TrimPrefix(path, "~/"))))
		}
	}
	return resolvePath(path)
}

// unknownCodex stands for the owner of a lex header that names no manifest path.
const unknownCodex = "unknown codex"

func rejectForeignOutput(codex, output string, data []byte) error {
	if owner, foreign := foreignOutputOwner(codex, data); foreign {
		return fmt.Errorf("%s: output belongs to codex %s, not codex %s", output, owner, resolvePath(codex))
	}
	return nil
}

// ForeignHeaderPhrase names the lex header of another codex for check and status lines.
func ForeignHeaderPhrase(owner string) string {
	if owner == unknownCodex {
		return "a lex header that names no codex"
	}
	return "the lex header of codex " + owner
}

// foreignOutputOwner reports the codex named by a lex header that is not this codex's ("unknown codex" when the header names none).
func foreignOutputOwner(codex string, data []byte) (string, bool) {
	marker := []byte("<!-- lex: собран из ")
	lines := bytes.SplitAfter(data, []byte("\n"))
	offset := 0
	count := frontMatterLength(lines)
	for _, line := range lines[:count] {
		offset += len(line)
	}
	lineEnd := bytes.IndexByte(data[offset:], '\n')
	line := data[offset:]
	if lineEnd >= 0 {
		line = line[:lineEnd]
	}
	value, ok := bytes.CutPrefix(line, marker)
	if !ok {
		return "", false
	}
	codex = resolvePath(codex)
	owner := unknownCodex
	if before, _, ok0 := bytes.Cut(value, []byte(".yaml. Правь фрагменты в ")); ok0 {
		manifestDir := []byte("/manifest/")
		if manifestIndex := bytes.LastIndex(before, manifestDir); manifestIndex >= 0 {
			ownerPath := string(value[:manifestIndex])
			owner = expandHeaderCodexPath(ownerPath)
			name := before[manifestIndex+len(manifestDir):]
			if owner == codex && len(name) > 0 && !bytes.Contains(name, []byte("/")) {
				return "", false
			}
		}
	}
	return owner, true
}

func withoutGeneratedHeader(data []byte, manifestPath string) []byte {
	stripped, ok := stripGeneratedHeader(data, manifestPath)
	if ok {
		return stripped
	}
	return data
}

func stripGeneratedHeader(data []byte, manifestPath string) ([]byte, bool) {
	lines := bytes.SplitAfter(data, []byte("\n"))
	count := frontMatterLength(lines)
	offset := 0
	for _, line := range lines[:count] {
		offset += len(line)
	}
	for _, marker := range generatedMarkerForms(manifestPath) {
		if bytes.HasPrefix(data[offset:], marker) {
			return append(append([]byte{}, data[:offset]...), data[offset+len(marker):]...), true
		}
	}
	return data, false
}

func equalOutputIgnoringGeneratedHeader(actual, want []byte, manifestPath string) bool {
	actual, actualHasHeader := stripGeneratedHeader(actual, manifestPath)
	want, wantHasHeader := stripGeneratedHeader(want, manifestPath)
	return actualHasHeader && wantHasHeader && bytes.Equal(actual, want)
}

func outputMatchesGenerated(actual, want []byte, header bool, manifestPath string) bool {
	return bytes.Equal(actual, want) || header && equalOutputIgnoringGeneratedHeader(actual, want, manifestPath)
}

func fragmentsChangedSinceBuild(codex, manifestPath string, m manifest, want []byte) bool {
	if m.builtHash != "" {
		return !generatedHashMatches(want, m.header, manifestPath, m.builtHash)
	}
	return validateRefresh(codex, manifestKey(manifestPath), m) != nil
}

func generatedHashMatches(want []byte, header bool, manifestPath, expectedHash string) bool {
	if sha(want) == expectedHash {
		return true
	}
	if !header {
		return false
	}
	if sha(withoutGeneratedHeader(want, manifestPath)) == expectedHash {
		return true
	}
	preferred := generatedMarker(manifestPath)
	for _, marker := range generatedMarkerForms(manifestPath) {
		if sha(bytes.Replace(want, preferred, marker, 1)) == expectedHash {
			return true
		}
	}
	return false
}

func outputHasManualEdits(readErr error, actual, want []byte, m manifest, path string, force bool) bool {
	return readErr == nil && sha(actual) != m.builtHash && !bytes.Equal(actual, want) && !force && !outputMatchesGenerated(actual, want, m.header, path)
}

func updateManifestField(path, key, value string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s: read manifest: %w", path, err)
	}
	lines := strings.SplitAfter(string(b), "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(line, key+": ") {
			lines[i] = key + ": " + value + "\n"
			found = true
		}
	}
	if !found {
		idx := 0
		for idx < len(lines) && !strings.HasPrefix(lines[idx], "parts:") {
			idx++
		}
		lines = append(lines[:idx], append([]string{key + ": " + value + "\n"}, lines[idx:]...)...)
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "")), fileMode)
}

func setManifestHeader(codex, key string, enabled bool) error {
	value := "false"
	if enabled {
		value = "true"
	}
	return updateManifestField(filepath.Join(codex, "manifest", codexKey(key)+".yaml"), "header", value)
}

type AbsorbOptions struct {
	Shared   bool
	Force    bool
	DraftDir string
}

func Absorb(codex, file string, options AbsorbOptions) error {
	codex, err := canonicalCodex(codex, false)
	if err != nil {
		return err
	}
	return absorbWithCanonicalCodex(codex, file, options)
}

func AbsorbWithPaths(codex, file string, options AbsorbOptions) ([]string, error) {
	codex, err := canonicalCodex(codex, false)
	if err != nil {
		return nil, err
	}
	before, err := snapshotCodexFiles(codex)
	if err != nil {
		return nil, err
	}
	err = Absorb(codex, file, options)
	after, snapshotErr := snapshotCodexFiles(codex)
	if snapshotErr != nil {
		return nil, errors.Join(err, snapshotErr)
	}
	return changedCodexFiles(before, after), err
}

func absorbWithCanonicalCodex(codex, file string, options AbsorbOptions) error {
	paths, err := filepath.Glob(filepath.Join(codex, "manifest", "*.yaml"))
	if err != nil {
		return fmt.Errorf("list manifests: %w", err)
	}
	target, err := filepath.Abs(file)
	if err != nil {
		return fmt.Errorf("resolve output path: %w", err)
	}
	actual, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	if err := rejectForeignOutput(codex, target, actual); err != nil {
		return err
	}
	sort.Strings(paths)
	selected, m, err := findOutputManifest(paths, target)
	if err != nil {
		return err
	}
	read := os.ReadFile
	built, err := manifestBodyWithRead(codex, m, read)
	if err != nil {
		return err
	}
	if m.header {
		built = generatedHeader(selected, built)
	}
	fragmentsChanged := fragmentsChangedSinceBuild(codex, selected, m, built)
	if fragmentsChanged && !outputMatchesGenerated(actual, built, m.header, selected) && !options.Force {
		return forceRefusal(codex, target, selected, paths, actual, m)
	}
	fileBytes := actual
	if m.header {
		built = withoutGeneratedHeader(built, selected)
		actual = withoutGeneratedHeader(actual, selected)
	}
	if bytes.Equal(actual, built) {
		return restoreDeletedHeader(selected, m, fileBytes, built)
	}
	warnSnapshot := snapshotAbsorbEditWarning(target, codex, selected, m, fileBytes)
	purposeEnabled, err := readPurposeSetting(codex, read)
	if err != nil {
		return err
	}
	var purpose *purposeLayout
	if purposeEnabled {
		layout, err := readPurposeLayoutWithRead(codex, m, read)
		if err != nil {
			return err
		}
		_, err = layout.strip(actual, m.out)
		if err != nil {
			return err
		}
		built = layout.rendered
		purpose = &layout
	} else {
		built, actual, err = stripPurposeForAbsorb(codex, m, actual, purposeEnabled)
		if err != nil {
			return err
		}
	}
	return warnAfterAbsorb(absorbEditedFragment(codex, paths, selected, m, built, actual, fileBytes, options, purpose), target, warnSnapshot)
}

// restoreDeletedHeader writes the build back into a file whose text equals the fragments but whose lex header line was deleted: nothing in the file is lost, and lex check names absorb for it.
func restoreDeletedHeader(manifestPath string, m manifest, fileBytes, body []byte) error {
	if !m.header {
		return nil
	}
	full := generatedHeader(manifestPath, body)
	if outputMatchesGenerated(fileBytes, full, true, manifestPath) {
		// A restore whose manifest write failed left the file right and the hash stale: record it now.
		if m.builtHash != "" && bytes.Equal(fileBytes, full) && sha(full) != m.builtHash {
			if err := updateManifestField(manifestPath, "built_sha256", sha(full)); err != nil {
				return fmt.Errorf("%s: record the restored file in the manifest: %w", m.out, err)
			}
		}
		return nil
	}
	if info, err := os.Lstat(m.out); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s: refusing to write through symlink", m.out)
	}
	if err := os.WriteFile(m.out, full, fileMode); err != nil {
		return fmt.Errorf("%s: %w", m.out, err)
	}
	if err := updateManifestField(manifestPath, "built_sha256", sha(full)); err != nil {
		return fmt.Errorf("%s: restored the lex header line, but the manifest was not updated: %w; run lex absorb again to record the file", m.out, err)
	}
	fmt.Fprintf(os.Stderr, "%s: restored the lex header line; the text already matched the fragments\n", m.out)
	return nil
}

// warnAfterAbsorb prints the edit warning only when the absorb succeeded.
func warnAfterAbsorb(err error, target string, warning *editWarningSnapshot) error {
	if err == nil && warning != nil {
		printEditWarning("absorb", target, warning)
	}
	return err
}

func snapshotAbsorbEditWarning(target, codex, selected string, m manifest, fileBytes []byte) *editWarningSnapshot {
	if !statusFileChanged(fileBytes, selected, m) {
		return nil
	}
	return snapshotEditWarning(target, codex, selected, m)
}

func findOutputManifest(paths []string, target string) (string, manifest, error) {
	for _, path := range paths {
		m, err := readManifest(path)
		if err != nil {
			return "", manifest{}, err
		}
		if m.out == target {
			return path, m, nil
		}
	}
	return "", manifest{}, fmt.Errorf("%s: no manifest output", target)
}

type lineHunk struct {
	oldStart, oldEnd int
	newStart, newEnd int
}

type preparedFragmentEdit struct {
	part, path string
	updated    []byte
	hash       string
}

func absorbEditedFragment(codex string, paths []string, selected string, m manifest, built, actual, fileBytes []byte, options AbsorbOptions, purpose *purposeLayout) error {
	oldLines, newLines := absorbLines(built), absorbLines(actual)
	var bounds [][2]int
	var err error
	if purpose != nil {
		bounds = make([][2]int, len(m.parts))
		for i, span := range purpose.spans {
			bounds[i] = [2]int{span.start, span.end}
		}
	} else {
		bounds, err = fragmentLineBounds(codex, m.parts)
	}
	if err != nil {
		return err
	}
	hunks := absorbHunks(oldLines, newLines, bounds)
	if purpose != nil {
		hunks = splitPurposeHunks(purpose.spans, purpose.owners, oldLines, newLines, hunks)
	}
	edits, additions, err := planAbsorbEdits(codex, paths, selected, m, bounds, oldLines, newLines, hunks, actual, options, purpose != nil)
	if err != nil {
		return err
	}
	plan, err := planAbsorbManifest(codex, paths, selected, m, bounds, edits, additions, newLines, fileBytes)
	if err != nil {
		return err
	}
	if err := verifyAbsorbPlan(codex, plan, actual, options.DraftDir, manifestKey(selected), m.out, fileBytes); err != nil {
		return err
	}
	return writeAbsorbPlan(selected, plan)
}

type absorbPlan struct {
	edits       []preparedFragmentEdit
	fragments   map[int][]plannedFragment
	manifest    manifest
	removePaths []string
}

func planAbsorbEdits(codex string, paths []string, selected string, m manifest, bounds [][2]int, oldLines, newLines [][]byte, hunks []lineHunk, actual []byte, options AbsorbOptions, printedPurpose bool) ([]preparedFragmentEdit, map[int][]lineHunk, error) {
	owners, deltas, additions, err := assignAbsorbHunks(bounds, m.parts, oldLines, hunks, options.DraftDir, manifestKey(selected), m.out, actual)
	if err != nil {
		return nil, nil, err
	}
	if err := validateEditedPartOccurrences(codex, m.parts, owners); err != nil {
		return nil, nil, err
	}
	edits, err := prepareFragmentEdits(codex, m.parts, bounds, owners, deltas, newLines, additions, printedPurpose)
	if err != nil {
		return nil, nil, err
	}
	editable, err := nonemptyEditedOwners(owners, m.parts, edits)
	if err != nil {
		return nil, nil, err
	}
	if err := validateSharedEdits(paths, m.parts, editable, options.Shared); err != nil {
		return nil, nil, err
	}
	return edits, additions, nil
}

func planAbsorbManifest(codex string, paths []string, selected string, m manifest, bounds [][2]int, edits []preparedFragmentEdit, additions map[int][]lineHunk, newLines [][]byte, fileBytes []byte) (absorbPlan, error) {
	updated, removePaths, err := applyAbsorbedEdits(paths, m, edits, fileBytes)
	if err != nil {
		return absorbPlan{}, err
	}
	fragments, err := planInsertedFragments(codex, selected, m.parts, additions, newLines)
	if err != nil {
		return absorbPlan{}, err
	}
	positions := slices.Sorted(maps.Keys(fragments))
	inserted := 0
	for _, position := range positions {
		at := survivingInsertionIndex(position, bounds, m.parts, updated.parts) + inserted
		ids := make([]string, 0, len(fragments[position]))
		for _, fragment := range fragments[position] {
			ids = append(ids, fragment.id)
			updated.fragmentHashes[fragment.id] = sha(fragment.raw)
		}
		updated.parts = append(updated.parts[:at], append(ids, updated.parts[at:]...)...)
		inserted += len(ids)
	}
	return absorbPlan{edits: edits, fragments: fragments, manifest: updated, removePaths: removePaths}, nil
}

func verifyAbsorbPlan(codex string, plan absorbPlan, actual []byte, draftDir, key, output string, fileBytes []byte) error {
	overrides := make(map[string][]byte, len(plan.edits)+len(plan.fragments))
	for _, edit := range plan.edits {
		overrides[edit.path] = edit.updated
	}
	for _, fragments := range plan.fragments {
		for _, fragment := range fragments {
			overrides[fragment.path] = fragment.raw
		}
	}
	read := func(path string) ([]byte, error) {
		if data, ok := overrides[path]; ok {
			return data, nil
		}
		return os.ReadFile(path)
	}
	rebuilt, err := manifestBodyWithRead(codex, plan.manifest, read)
	if err != nil {
		return err
	}
	if bytes.Equal(rebuilt, actual) {
		return nil
	}
	line, fileLine, rebuiltLine := firstDiff(actual, rebuilt)
	reason := fmt.Sprintf("absorbed fragments would not rebuild this file: first differing line %d: file %q, rebuilt %q", line, trunc(fileLine), trunc(rebuiltLine))
	return writeAbsorbDraft(draftDir, key, output, fileBytes, reason)
}

func writeAbsorbPlan(selected string, plan absorbPlan) error {
	for _, edit := range plan.edits {
		body, err := fragmentBody(edit.updated)
		if err != nil {
			return fmt.Errorf("%s: %w", edit.path, err)
		}
		if len(body) == 0 {
			continue
		}
		if err := os.WriteFile(edit.path, edit.updated, fileMode); err != nil {
			return fmt.Errorf("%s: write fragment: %w", edit.path, err)
		}
	}
	for _, fragments := range plan.fragments {
		for _, fragment := range fragments {
			if err := os.MkdirAll(filepath.Dir(fragment.path), directoryMode); err != nil {
				return fmt.Errorf("%s: create fragment directory: %w", filepath.Dir(fragment.path), err)
			}
			if err := os.WriteFile(fragment.path, fragment.raw, fileMode); err != nil {
				return fmt.Errorf("%s: write new fragment: %w", fragment.path, err)
			}
		}
	}
	if err := writeManifest(selected, plan.manifest, plan.manifest.links); err != nil {
		return err
	}
	return removeAbsorbedFragments(plan.removePaths)
}

func splitPurposeHunks(spans []purposeSpan, owners map[int]string, oldLines, newLines [][]byte, hunks []lineHunk) []lineHunk {
	result := make([]lineHunk, 0, len(hunks))
	for _, hunk := range hunks {
		result = append(result, splitPurposeHunk(spans, owners, oldLines, newLines, hunk)...)
	}
	return result
}

func splitPurposeHunk(spans []purposeSpan, owners map[int]string, oldLines, newLines [][]byte, hunk lineHunk) []lineHunk {
	start, end := hunk.oldStart, hunk.oldEnd
	for _, span := range spans {
		if span.end <= start || span.start >= end {
			continue
		}
		if !purposeSpanCovered(owners, oldLines, span, start, end) {
			break
		}
		start = min(end, span.end)
	}
	for i := len(spans) - 1; i >= 0; i-- {
		span := spans[i]
		if span.start >= end {
			continue
		}
		if span.end <= start {
			break
		}
		if !purposeSpanCovered(owners, oldLines, span, start, end) {
			break
		}
		end = max(start, span.start)
	}
	if start == hunk.oldStart && end == hunk.oldEnd || start >= end {
		return []lineHunk{hunk}
	}
	receiver, ok := purposeReceiverSpan(spans, start, end)
	added := newLines[hunk.newStart:hunk.newEnd]
	if !ok || len(split(bytes.Join(added, nil))) > len(split(bytes.Join(oldLines[start:end], nil))) || !purposeReceiverAccepts(owners, oldLines[receiver.start:receiver.end], added, receiver) {
		return []lineHunk{hunk}
	}
	result := make([]lineHunk, 0, 3)
	if start > hunk.oldStart {
		result = append(result, lineHunk{oldStart: hunk.oldStart, oldEnd: start, newStart: hunk.newStart, newEnd: hunk.newStart})
	}
	result = append(result, lineHunk{oldStart: start, oldEnd: end, newStart: hunk.newStart, newEnd: hunk.newEnd})
	if end < hunk.oldEnd {
		result = append(result, lineHunk{oldStart: end, oldEnd: hunk.oldEnd, newStart: hunk.newEnd, newEnd: hunk.newEnd})
	}
	return result
}

func purposeSpanCovered(owners map[int]string, oldLines [][]byte, span purposeSpan, start, end int) bool {
	if _, ok := ownerForPurposeSpan(owners, span); !ok {
		return false
	}
	for i := span.start; i < span.end; i++ {
		if strings.TrimSpace(string(oldLines[i])) != "" && (i < start || i >= end) {
			return false
		}
	}
	return true
}

func ownerForPurposeSpan(owners map[int]string, span purposeSpan) (string, bool) {
	for i := span.start; i < span.end; i++ {
		if id, ok := owners[i]; ok {
			return id, true
		}
	}
	return "", false
}

// purposeReceiverSpan returns the one fragment that holds the rest [start, end) of a split hunk.
func purposeReceiverSpan(spans []purposeSpan, start, end int) (purposeSpan, bool) {
	for _, span := range spans {
		if span.start <= start && end <= span.end {
			return span, true
		}
	}
	return purposeSpan{}, false
}

// purposeReceiverAccepts tells whether the new lines can belong only to the receiving fragment: its own purpose line
// anchors it, or it is a heading and every new line is a heading. Otherwise the same text could be the removed
// neighbour's rule, edited and stripped of its purpose line.
func purposeReceiverAccepts(owners map[int]string, receiverLines, added [][]byte, receiver purposeSpan) bool {
	if _, ok := ownerForPurposeSpan(owners, receiver); ok {
		return true
	}
	return headingLines(receiverLines) && headingLines(added)
}

// headingLines reports whether the lines hold at least one heading and nothing else but blank lines.
func headingLines(lines [][]byte) bool {
	found := false
	for _, line := range lines {
		text := bytes.TrimSpace(line)
		if len(text) == 0 {
			continue
		}
		if text[0] != '#' {
			return false
		}
		found = true
	}
	return found
}

func assignAbsorbHunks(bounds [][2]int, parts []string, oldLines [][]byte, hunks []lineHunk, draftDir, key, output string, actual []byte) ([]int, []int, map[int][]lineHunk, error) {
	owners := make([]int, 0, len(hunks))
	deltas := make([]int, len(parts))
	additions := make(map[int][]lineHunk)
	for _, hunk := range hunks {
		assignment, ok := assignAbsorbHunk(bounds, oldLines, hunk)
		if !ok {
			return nil, nil, nil, writeAbsorbDraft(draftDir, key, output, actual, "boundary conflict")
		}
		for _, owner := range assignment.owners {
			owners = appendUniqueInt(owners, owner)
		}
		for owner, delta := range assignment.deltas {
			deltas[owner] += delta
			if delta != 0 {
				owners = appendUniqueInt(owners, owner)
			}
		}
		if assignment.addition != nil {
			additions[assignment.additionPosition] = append(additions[assignment.additionPosition], *assignment.addition)
		}
	}
	return owners, deltas, additions, nil
}

type absorbHunkAssignment struct {
	owners           []int
	deltas           map[int]int
	additionPosition int
	addition         *lineHunk
}

func assignAbsorbHunk(bounds [][2]int, oldLines [][]byte, hunk lineHunk) (absorbHunkAssignment, bool) {
	if hunk.oldStart == hunk.oldEnd && hunk.newEnd > hunk.newStart {
		if part, tail, ok := insertionBoundaryPart(bounds, oldLines, hunk.oldStart); ok {
			hunk.newEnd += tail
			position := bounds[part][1]
			if hunk.oldStart == 0 {
				position = bounds[part][0]
			}
			return absorbHunkAssignment{deltas: map[int]int{part: -tail}, additionPosition: position, addition: &hunk}, true
		}
	}
	if hunk.newEnd > hunk.newStart {
		if part := replacedBoundaryBlank(bounds, oldLines, hunk); part >= 0 {
			return absorbHunkAssignment{owners: []int{part}, deltas: map[int]int{part: hunk.oldStart - hunk.oldEnd}, additionPosition: bounds[part][1], addition: &hunk}, true
		}
	}
	owner := fragmentForEdit(bounds, hunk.oldStart, hunk.oldEnd, hunk.newEnd-hunk.newStart > hunk.oldEnd-hunk.oldStart)
	if owner < 0 && hunk.newStart == hunk.newEnd {
		removed := deletedFragmentOwners(bounds, oldLines, hunk)
		if len(removed) > 0 {
			deltas := make(map[int]int, len(removed))
			for _, part := range removed {
				lo, hi := max(hunk.oldStart, bounds[part][0]), min(hunk.oldEnd, bounds[part][1])
				deltas[part] = lo - hi
			}
			return absorbHunkAssignment{owners: removed, deltas: deltas}, true
		}
	}
	if owner < 0 {
		return absorbHunkAssignment{}, false
	}
	delta := (hunk.newEnd - hunk.newStart) - (hunk.oldEnd - hunk.oldStart)
	return absorbHunkAssignment{owners: []int{owner}, deltas: map[int]int{owner: delta}}, true
}

func appendUniqueInt(values []int, value int) []int {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}

func nonemptyEditedOwners(owners []int, parts []string, edits []preparedFragmentEdit) ([]int, error) {
	var editable []int
	for _, owner := range owners {
		for _, edit := range edits {
			if edit.part != parts[owner] {
				continue
			}
			body, err := fragmentBody(edit.updated)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", edit.path, err)
			}
			if len(body) > 0 {
				editable = appendUniqueInt(editable, owner)
			}
			break
		}
	}
	return editable, nil
}

func applyAbsorbedEdits(paths []string, m manifest, edits []preparedFragmentEdit, fileBytes []byte) (manifest, []string, error) {
	updated := m
	updated.parts = append([]string(nil), m.parts...)
	updated.fragmentHashes = make(map[string]string, len(m.fragmentHashes))
	maps.Copy(updated.fragmentHashes, m.fragmentHashes)
	var removePaths []string
	for _, edit := range edits {
		body, err := fragmentBody(edit.updated)
		if err != nil {
			return manifest{}, nil, fmt.Errorf("%s: %w", edit.path, err)
		}
		if len(body) == 0 {
			updated.parts = slices.DeleteFunc(updated.parts, func(id string) bool { return id == edit.part })
			delete(updated.fragmentHashes, edit.part)
			affected, err := manifestsUsing(paths, edit.part)
			if err != nil {
				return manifest{}, nil, err
			}
			if len(affected) <= 1 {
				removePaths = append(removePaths, edit.path)
			}
			continue
		}
		updated.fragmentHashes[edit.part] = edit.hash
	}
	updated.builtHash = sha(fileBytes)
	return updated, removePaths, nil
}

// survivingInsertionIndex translates a boundary in the original rendered line
// stream to the corresponding index after empty fragments have been removed.
func survivingInsertionIndex(position int, bounds [][2]int, originalParts, remainingParts []string) int {
	originalAt := 0
	for i, b := range bounds {
		if position >= b[1] {
			originalAt = i + 1
		}
	}
	remainingAt := 0
	for i := 0; i < min(originalAt, len(originalParts)); i++ {
		if slices.Contains(remainingParts, originalParts[i]) {
			remainingAt++
		}
	}
	return remainingAt
}

func removeAbsorbedFragments(paths []string) error {
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("%s: remove fragment: %w", path, err)
		}
	}
	return nil
}

func insertionBoundaryPart(bounds [][2]int, oldLines [][]byte, position int) (int, int, bool) {
	if position == 0 && len(bounds) > 0 {
		return 0, 0, true
	}
	for i, b := range bounds {
		if position < b[0] || position > b[1] {
			continue
		}
		tail, blank := b[1]-position, true
		for _, line := range oldLines[position:b[1]] {
			if strings.TrimSpace(string(line)) != "" {
				blank = false
				break
			}
		}
		if blank {
			return i, tail, true
		}
	}
	return -1, 0, false
}

func replacedBoundaryBlank(bounds [][2]int, oldLines [][]byte, hunk lineHunk) int {
	if hunk.oldStart == hunk.oldEnd || hunk.newStart == hunk.newEnd {
		return -1
	}
	for i, b := range bounds {
		if hunk.oldEnd != b[1] || hunk.oldStart < b[0] {
			continue
		}
		blank := true
		for _, line := range oldLines[hunk.oldStart:hunk.oldEnd] {
			if strings.TrimSpace(string(line)) != "" {
				blank = false
				break
			}
		}
		if blank {
			return i
		}
	}
	return -1
}

func deletedFragmentOwners(bounds [][2]int, oldLines [][]byte, hunk lineHunk) []int {
	var fullyDeleted []int
	for i, b := range bounds {
		if hunk.oldStart <= b[0] && hunk.oldEnd >= b[1] && b[1] > b[0] {
			fullyDeleted = append(fullyDeleted, i)
		}
	}
	if len(fullyDeleted) == 0 {
		return nil
	}
	for i, b := range bounds {
		lo, hi := max(hunk.oldStart, b[0]), min(hunk.oldEnd, b[1])
		if lo >= hi || slices.Contains(fullyDeleted, i) {
			continue
		}
		for _, line := range oldLines[lo:hi] {
			if strings.TrimSpace(string(line)) != "" {
				return nil
			}
		}
		fullyDeleted = append(fullyDeleted, i)
	}
	sort.Ints(fullyDeleted)
	return fullyDeleted
}

type plannedFragment struct {
	id, path string
	raw      []byte
}

func planInsertedFragments(codex, selected string, parts []string, additions map[int][]lineHunk, newLines [][]byte) (map[int][]plannedFragment, error) {
	created := make(map[int][]plannedFragment)
	counts := make(map[string]int)
	for _, part := range parts {
		base := filepath.Base(part)
		if dash := strings.LastIndexByte(base, '-'); dash >= 0 {
			if n, err := strconv.Atoi(base[dash+1:]); err == nil {
				label := base[:dash]
				if n > counts[label] {
					counts[label] = n
				}
			}
		}
	}
	for position, hunks := range additions {
		sort.Slice(hunks, func(i, j int) bool { return hunks[i].newStart < hunks[j].newStart })
		for _, hunk := range hunks {
			body := bytes.Join(newLines[hunk.newStart:hunk.newEnd], nil)
			for _, unit := range split(body) {
				label := slug(unit)
				if label == "" {
					label = "fragment"
				}
				counts[label]++
				id := fmt.Sprintf("%s/%s-%d", manifestKey(selected), label, counts[label])
				path := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
				raw := append([]byte("---\nid: "+id+"\nцель: \nоснование: \nснять_когда: \n---\n"), unit...)
				created[position] = append(created[position], plannedFragment{id: id, path: path, raw: raw})
			}
		}
	}
	return created, nil
}

func absorbLines(data []byte) [][]byte {
	if len(data) == 0 {
		return nil
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	if len(lines[len(lines)-1]) == 0 {
		return lines[:len(lines)-1]
	}
	return lines
}

func absorbHunks(oldLines, newLines [][]byte, bounds [][2]int) []lineHunk {
	from, to, newFrom, newTo := changedLineRanges(oldLines, newLines)
	owner := fragmentForEdit(bounds, from, to, newTo-newFrom > to-from)
	if owner >= 0 {
		return []lineHunk{{oldStart: from, oldEnd: to, newStart: newFrom, newEnd: newTo}}
	}
	return changedLineHunks(oldLines, newLines)
}

func changedLineRanges(oldLines, newLines [][]byte) (int, int, int, int) {
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && bytes.Equal(oldLines[prefix], newLines[prefix]) {
		prefix++
	}
	suffix := 0
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix && bytes.Equal(oldLines[len(oldLines)-1-suffix], newLines[len(newLines)-1-suffix]) {
		suffix++
	}
	return prefix, len(oldLines) - suffix, prefix, len(newLines) - suffix
}

func validateEditedPartOccurrences(codex string, parts []string, owners []int) error {
	counts := make(map[string]int, len(parts))
	for _, part := range parts {
		counts[part]++
	}
	for _, owner := range owners {
		part := parts[owner]
		if counts[part] > 1 {
			path := filepath.Join(codex, "frag", filepath.FromSlash(part)+".md")
			return fmt.Errorf("%s: fragment occurs more than once in manifest; cannot absorb distinct edits", path)
		}
	}
	return nil
}

func validateSharedEdits(paths []string, parts []string, owners []int, shared bool) error {
	checked := make(map[string]bool)
	for _, owner := range owners {
		part := parts[owner]
		if checked[part] {
			continue
		}
		checked[part] = true
		affected, err := manifestsUsing(paths, part)
		if err != nil {
			return err
		}
		if len(affected) > 1 && !shared {
			return fmt.Errorf("shared fragment affects: %s; pass --shared", strings.Join(affected, ", "))
		}
	}
	return nil
}

func prepareFragmentEdits(codex string, parts []string, bounds [][2]int, owners, deltas []int, newLines [][]byte, additions map[int][]lineHunk, printedPurpose bool) ([]preparedFragmentEdit, error) {
	edits := make([]preparedFragmentEdit, 0)
	lineShift := 0
	appliedAdditions := make(map[int]bool, len(additions))
	for i, part := range parts {
		for position, hunks := range additions {
			if position <= bounds[i][0] && !appliedAdditions[position] {
				for _, hunk := range hunks {
					lineShift += hunk.newEnd - hunk.newStart
				}
				appliedAdditions[position] = true
			}
		}
		if !slices.Contains(owners, i) {
			continue
		}
		lo := bounds[i][0] + lineShift
		hi := lo + (bounds[i][1] - bounds[i][0]) + deltas[i]
		path := filepath.Join(codex, "frag", filepath.FromSlash(part)+".md")
		if lo < 0 || hi < lo || hi > len(newLines) {
			return nil, fmt.Errorf("%s: invalid edited fragment line bounds", path)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		body, err := fragmentBody(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		replacement := bytes.Join(newLines[lo:hi], nil)
		if printedPurpose {
			replacement = stripPurposeLines(replacement)
		}
		updated := absorbReplaceFragmentBody(raw, body, replacement)
		edits = append(edits, preparedFragmentEdit{part: part, path: path, updated: updated, hash: sha(updated)})
		lineShift += deltas[i]
	}
	return edits, nil
}

func stripPurposeLines(data []byte) []byte {
	var result []byte
	for _, line := range absorbLines(data) {
		if !bytes.HasPrefix(line, []byte(purposeMarker)) {
			result = append(result, line...)
		}
	}
	return result
}

// changedLineHunks groups edits separated by at least one unchanged line.
func changedLineHunks(oldLines, newLines [][]byte) []lineHunk {
	matches := myersLineMatches(oldLines, newLines)
	hunks := make([]lineHunk, 0)
	oldPos, newPos := 0, 0
	for _, match := range matches {
		if oldPos != match[0] || newPos != match[1] {
			hunks = append(hunks, lineHunk{oldStart: oldPos, oldEnd: match[0], newStart: newPos, newEnd: match[1]})
		}
		oldPos, newPos = match[0]+1, match[1]+1
	}
	if oldPos != len(oldLines) || newPos != len(newLines) {
		hunks = append(hunks, lineHunk{oldStart: oldPos, oldEnd: len(oldLines), newStart: newPos, newEnd: len(newLines)})
	}
	return hunks
}

func myersLineMatches(oldLines, newLines [][]byte) [][2]int {
	n, m := len(oldLines), len(newLines)
	max := n + m
	offset := max + 1
	v := make([]int, 2*max+3)
	trace := make([][]int, 0, max+1)
	distance := 0
	for d := 0; d <= max; d++ {
		trace = append(trace, append([]int(nil), v...))
		if myersDistanceStep(v, offset, d, oldLines, newLines) {
			distance = d
			break
		}
	}
	matches := backtrackMyers(trace, distance, offset, n, m)
	slices.Reverse(matches)
	return matches
}

func myersDistanceStep(v []int, offset, d int, oldLines, newLines [][]byte) bool {
	n, m := len(oldLines), len(newLines)
	for k := -d; k <= d; k += 2 {
		idx := offset + k
		x := 0
		if k == -d || (k != d && v[idx-1] < v[idx+1]) {
			x = v[idx+1]
		} else {
			x = v[idx-1] + 1
		}
		y := x - k
		for x < n && y < m && bytes.Equal(oldLines[x], newLines[y]) {
			x++
			y++
		}
		v[idx] = x
		if x >= n && y >= m {
			return true
		}
	}
	return false
}

func backtrackMyers(trace [][]int, distance, offset, n, m int) [][2]int {
	matches := make([][2]int, 0)
	x, y := n, m
	for d := distance; d >= 0; d-- {
		v := trace[d]
		k := x - y
		idx := offset + k
		prevK := 0
		if k == -d || (k != d && v[idx-1] < v[idx+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := v[offset+prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x--
			y--
			matches = append(matches, [2]int{x, y})
		}
		x, y = prevX, prevY
	}
	return matches
}

func fragmentForEdit(bounds [][2]int, from, to int, inserts bool) int {
	owner := fragmentOwnerForLines(bounds, from, to)
	if owner < 0 && inserts && from == to {
		for i, b := range bounds {
			if from > b[0] && from < b[1] {
				return i
			}
			if from == 0 && i == 0 {
				return i
			}
			if from == b[1] && i == len(bounds)-1 {
				return i
			}
		}
	}
	return owner
}
func fragmentOwnerForLines(bounds [][2]int, from, to int) int {
	owner := -1
	for line := from; line < to; line++ {
		for i, b := range bounds {
			if line >= b[0] && line < b[1] {
				if owner >= 0 && owner != i {
					return -1
				}
				owner = i
			}
		}
	}
	return owner
}
func fragmentLineBounds(codex string, parts []string) ([][2]int, error) {
	bounds := make([][2]int, 0, len(parts))
	start := 0
	for _, id := range parts {
		path := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		body, err := fragmentBody(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		count := bytes.Count(body, []byte("\n"))
		if len(body) > 0 && body[len(body)-1] != '\n' {
			count++
		}
		bounds = append(bounds, [2]int{start, start + count})
		start += count
	}
	return bounds, nil
}

func manifestsUsing(paths []string, part string) ([]string, error) {
	var outputs []string
	for _, path := range paths {
		m, err := readManifest(path)
		if err != nil {
			return nil, err
		}
		if slices.Contains(m.parts, part) {
			outputs = append(outputs, m.out)
		}
	}
	return outputs, nil
}

// forceRefusal says why absorb refuses a file whose fragments changed since the build, with the commands that keep either side.
func forceRefusal(codex, target, selected string, paths []string, actual []byte, m manifest) error {
	codexArg, fileArg := strconv.Quote(codex), strconv.Quote(target)
	sharedFlag, sharedHint, hintErr := sharedForceHint(paths, m.out, m.parts)
	if hintErr != nil {
		return hintErr
	}
	fileChanged := statusFileChanged(actual, selected, m)
	if fileChanged {
		conflict := "file and fragments both changed since the last build"
		if m.builtHash == "" {
			conflict = "the file differs from its recorded source and the fragments differ from the manifest"
		}
		return fmt.Errorf("%s: %s; run `lex absorb --force%s --codex %s %s` to keep the file and lose fragment text edits not in it, or `lex build --force --codex %s %s` to keep the fragments and lose file edits%s", target, conflict, sharedFlag, codexArg, fileArg, codexArg, fileArg, sharedHint)
	}
	conflict := "fragments changed since the last build"
	if m.builtHash == "" {
		conflict = "the fragments differ from the manifest"
	}
	return fmt.Errorf("%s: %s; run `lex build --codex %s %s` to carry fragment edits into the file, or `lex absorb --force%s --codex %s %s` to discard them%s", target, conflict, codexArg, fileArg, sharedFlag, codexArg, fileArg, sharedHint)
}

// sharedForceHint returns " --shared" and the sentence naming the other outputs
// when a part of the output is in another manifest too, so that the absorb
// --force command named beside it runs as written; both are empty otherwise.
func sharedForceHint(paths []string, selected string, parts []string) (string, string, error) {
	var others []string
	seen := make(map[string]bool)
	for _, part := range parts {
		outputs, err := manifestsUsing(paths, part)
		if err != nil {
			return "", "", err
		}
		for _, output := range outputs {
			if output != selected && !seen[output] {
				seen[output] = true
				others = append(others, output)
			}
		}
	}
	sort.Strings(others)
	if len(others) == 0 {
		return "", "", nil
	}
	return " --shared", "; --shared also rewrites the shared fragment that builds " + strings.Join(others, ", ") + ": their edits to it not in this file are lost", nil
}

// codexSharedForceHint is sharedForceHint over the manifests of codex.
func codexSharedForceHint(codex string, m manifest) (string, string, error) {
	paths, err := filepath.Glob(filepath.Join(codex, "manifest", "*.yaml"))
	if err != nil {
		return "", "", fmt.Errorf("list manifests: %w", err)
	}
	sort.Strings(paths)
	return sharedForceHint(paths, m.out, m.parts)
}

func manifestKey(path string) string { return strings.TrimSuffix(filepath.Base(path), ".yaml") }
func replaceFragmentBody(file, old, repl []byte) []byte {
	header := bytes.TrimSuffix(file, old)
	if bytes.HasPrefix(header, []byte("---\n")) {
		if closeAt := bytes.Index(header[4:], []byte("---\n")); closeAt >= 0 {
			frontMatter := header[:4+closeAt]
			frontMatter = bytes.Replace(frontMatter, []byte("draft: true\n"), nil, 1)
			header = append(frontMatter, header[4+closeAt:]...)
		}
	}
	return append(header, repl...)
}

var absorbReplaceFragmentBody = replaceFragmentBody

func fragmentIsDraft(fragment []byte) bool {
	if !bytes.HasPrefix(fragment, []byte("---\n")) {
		return false
	}
	closing := bytes.Index(fragment[4:], []byte("---\n"))
	if closing < 0 {
		return false
	}
	header := fragment[4 : 4+closing]
	for line := range bytes.SplitSeq(header, []byte("\n")) {
		if bytes.Equal(bytes.TrimSpace(line), []byte("draft: true")) {
			return true
		}
	}
	return false
}
func writeAbsorbDraft(dir, key, output string, data []byte, reason string) error {
	if dir == "" {
		return fmt.Errorf("%s: %s; draft directory must be provided", output, reason)
	}
	if err := os.MkdirAll(filepath.Join(dir, "drafts"), directoryMode); err != nil {
		return fmt.Errorf("create draft directory: %w", err)
	}
	name := manifestKey(key) + "-" + time.Now().UTC().Format("20060102T150405.000000000Z") + ".md"
	p := filepath.Join(dir, "drafts", name)
	if err := os.WriteFile(p, data, fileMode); err != nil {
		return fmt.Errorf("%s: write draft: %w", p, err)
	}
	return fmt.Errorf("%s: %s; draft saved to %s", output, reason, p)
}

type CheckOptions struct {
	Staged       bool
	OutputsUnder string
	Tree         string
	RefsOnly     bool
	Decisions    string
}

func CheckWithWarnings(codex string, options CheckOptions) ([]string, error) {
	codex, err := canonicalCodex(codex, false)
	if err != nil {
		return nil, err
	}
	if options.Tree != "" && options.OutputsUnder == "" {
		return nil, errors.New("--tree requires --outputs-under")
	}
	if options.Staged && options.OutputsUnder != "" {
		return nil, errors.New("--staged cannot be combined with --outputs-under")
	}
	var warnings []string
	var checkErr error
	if !options.RefsOnly {
		switch {
		case options.Staged:
			warnings, checkErr = checkStaged(codex)
		case options.OutputsUnder != "":
			warnings, checkErr = checkDiskUnder(codex, options.OutputsUnder, options.Tree)
		default:
			warnings, checkErr = checkDisk(codex)
		}
		if checkErr != nil {
			return warnings, checkErr
		}
	}
	refWarnings, refErr := checkTextRefs(codex, options)
	warnings = append(warnings, refWarnings...)
	return warnings, errors.Join(checkErr, refErr)
}

func resolveOptionalTree(tree string) (string, error) {
	if tree == "" {
		return "", nil
	}
	return resolveTree(tree)
}

func resolveTree(tree string) (string, error) {
	abs, err := filepath.Abs(tree)
	if err != nil {
		return "", fmt.Errorf("%s: resolve tree path: %w", tree, err)
	}
	tree = resolvePath(abs)
	info, err := os.Stat(tree)
	if err != nil {
		return "", fmt.Errorf("%s: %w", tree, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s: tree path is not a directory", tree)
	}
	return tree, nil
}

func relativeOutput(underText, under, out string) (string, error) {
	rel, err := filepath.Rel(underText, out)
	if err != nil {
		return "", fmt.Errorf("compute output path relative to %s: %w", underText, err)
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return rel, nil
	}
	rel, err = filepath.Rel(under, out)
	if err != nil {
		return "", fmt.Errorf("compare output path %s with %s: %w", out, under, err)
	}
	return rel, nil
}

func outputInTree(tree, rel, out string) string {
	if tree == "" {
		return out
	}
	return filepath.Join(tree, rel)
}

func readCheckOutput(tree, path string) ([]byte, error) {
	if tree == "" {
		if err := rejectSymlinkParents(path); err != nil {
			return nil, err
		}
		return os.ReadFile(path)
	}
	return readTreeOutput(path)
}

func readTreeOutput(path string) ([]byte, error) {
	if err := rejectSymlinkParents(path); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect tree output %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s: refusing to read through symlink", path)
	}
	return os.ReadFile(path)
}

func rejectSymlinkParents(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("%s: resolve output path: %w", path, err)
	}
	current := filepath.VolumeName(abs) + string(filepath.Separator)
	parts := strings.Split(strings.TrimPrefix(abs, current), string(filepath.Separator))
	for _, part := range parts[:max(0, len(parts)-1)] {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("%s: inspect output path: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s: refusing to read through symlink", current)
		}
	}
	return nil
}

type selectedManifestUnder struct {
	path   string
	m      manifest
	rel    string
	output bool
	links  []outputLink
}

func checkDiskUnder(codex, under, tree string) ([]string, error) {
	abs, err := filepath.Abs(under)
	if err != nil {
		return nil, fmt.Errorf("%s: resolve output path: %w", under, err)
	}
	underText := filepath.Clean(abs)
	under = resolvePath(abs)
	tree, err = resolveOptionalTree(tree)
	if err != nil {
		return nil, err
	}
	paths, err := filepath.Glob(filepath.Join(codex, "manifest", "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("list manifests: %w", err)
	}
	sort.Strings(paths)
	selected, warnings, problems, err := selectManifestsUnder(paths, underText, under, tree)
	if err != nil {
		return nil, err
	}
	for _, entry := range selected {
		problems = append(problems, diskLinkProblems(entry.links, tree != "")...)
	}
	return checkSelectedOutputs(codex, tree, selected, warnings, problems)
}

func selectManifestsUnder(paths []string, underText, under, tree string) ([]*selectedManifestUnder, []string, []string, error) {
	selected := make([]*selectedManifestUnder, 0, len(paths))
	var warnings, problems []string
	for _, path := range paths {
		m, manifestErr := readManifest(path)
		if manifestErr != nil && m.out == "" {
			problems = append(problems, invalidManifestProblem(path, manifestErr))
			continue
		}
		outputInScope, scopedLinks, err := manifestScope(m, under, tree)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", path, err)
		}
		if !outputInScope && len(scopedLinks) == 0 {
			continue
		}
		if manifestErr != nil {
			problems = append(problems, invalidManifestProblem(path, manifestErr))
			continue
		}
		entry := &selectedManifestUnder{path: path, m: m, output: outputInScope}
		if outputInScope {
			out, err := filepath.Abs(m.out)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("%s: resolve output path: %w", path, err)
			}
			scopeOut := resolvePath(out)
			if tree != "" {
				scopeOut = out
			}
			entry.rel, err = relativeOutput(underText, under, scopeOut)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("%s: %w", path, err)
			}
		}
		for _, link := range scopedLinks {
			if tree == "" {
				entry.links = append(entry.links, link)
				continue
			}
			rel, err := relativeOutput(underText, under, link.path)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("%s: %w", path, err)
			}
			entry.links = append(entry.links, outputLink{path: filepath.Join(tree, rel), target: link.target})
		}
		selected = append(selected, entry)
	}
	return selected, warnings, problems, nil
}

func checkSelectedOutputs(codex, tree string, selected []*selectedManifestUnder, warnings, problems []string) ([]string, error) {
	hasOutput := false
	for _, entry := range selected {
		hasOutput = hasOutput || entry.output
	}
	if !hasOutput {
		return finishCheck(warnings, problems)
	}
	checker, err := loadToolChecker(codex)
	if err != nil {
		return nil, err
	}
	purposeEnabled, err := diskPurposeSetting(codex)
	if err != nil {
		return nil, err
	}
	checker.purpose = purposeEnabled
	referenced := map[string]bool{}
	for _, entry := range selected {
		if !entry.output {
			continue
		}
		read := func(path string) ([]byte, error) {
			b, err := os.ReadFile(path)
			if err == nil {
				w, p := checker.checkWithWarnings(path, b)
				warnings = append(warnings, w...)
				problems = append(problems, p...)
			}
			return b, err
		}
		body, missing, draftWarnings := checkedManifestBody(codex, entry.m, referenced, read, purposeEnabled)
		warnings = append(warnings, draftWarnings...)
		problems = append(problems, missing...)
		if entry.m.header {
			body = generatedHeader(entry.path, body)
		}
		out := outputInTree(tree, entry.rel, entry.m.out)
		data, err := readCheckOutput(tree, out)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		if !outputMatchesGenerated(data, body, entry.m.header, entry.path) {
			problems = append(problems, builtDiffers(out, codex, entry.m, entry.path, body, data))
		}
	}
	return finishCheck(warnings, problems)
}

// checkDisk - сверка собранных файлов на диске со всем кодексом.
func checkDisk(codex string) ([]string, error) {
	checker, err := loadToolChecker(codex)
	if err != nil {
		return nil, err
	}
	purposeEnabled, err := diskPurposeSetting(codex)
	if err != nil {
		return nil, err
	}
	checker.purpose = purposeEnabled
	paths, err := filepath.Glob(filepath.Join(codex, "manifest", "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("list manifests: %w", err)
	}
	sort.Strings(paths)
	referenced := map[string]bool{}
	var warnings, problems []string
	for _, path := range paths {
		m, err := readManifest(path)
		if err != nil {
			problems = append(problems, invalidManifestProblem(path, err))
			continue
		}
		problems = append(problems, diskLinkProblems(m.links)...)
		body, missing, draftWarnings := checkedManifestBody(codex, m, referenced, os.ReadFile, purposeEnabled)
		warnings = append(warnings, draftWarnings...)
		problems = append(problems, missing...)
		if m.header {
			body = generatedHeader(path, body)
		}
		data, err := os.ReadFile(m.out)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		if !outputMatchesGenerated(data, body, m.header, path) {
			problems = append(problems, builtDiffers(m.out, codex, m, path, body, data))
		}
	}
	if err := appendOrphanProblem(codex, referenced); err != nil {
		problems = append(problems, err.Error())
	}
	recordWarnings, err := diskRecordWarnings(codex, referenced)
	if err != nil {
		problems = append(problems, err.Error())
	}
	warnings = append(warnings, recordWarnings...)
	toolWarnings, toolProblems := checker.checkDiskFragmentsWithWarnings(codex, referenced)
	warnings = append(warnings, toolWarnings...)
	problems = append(problems, toolProblems...)
	return finishCheck(warnings, problems)
}

// checkStaged - проверка коммита (pre-commit): только то, что в него входит, и версии из индекса.
// Собранный файл проверяется, если в коммите он сам, его манифест или любой его фрагмент; фрагменты и манифест
// читаются из индекса, так что незакоммиченная работа другой сессии (диск) чужой коммит не останавливает,
// а закоммиченный собранный файл совпадает с закоммиченными фрагментами.
func checkStaged(codex string) ([]string, error) {
	checker, err := loadToolChecker(codex)
	if err != nil {
		return nil, err
	}
	commit, err := readStagedPaths()
	if err != nil {
		return nil, err
	}
	purposePath := filepath.Join(codex, "purpose.json")
	readSetting := func(path string) ([]byte, error) {
		if commit.rel(path) != "" {
			if _, ok := commit.index[commit.rel(path)]; !ok {
				return nil, os.ErrNotExist
			}
		}
		return commit.read(path)
	}
	checker.purpose, err = readPurposeSetting(codex, readSetting)
	if err != nil {
		return nil, err
	}
	if commit.has(purposePath) {
		head, headErr := exec.Command("git", "-C", commit.root, "show", "HEAD:"+commit.rel(purposePath)).Output()
		if headErr == nil {
			headValue, e := readPurposeSetting(codex, func(string) ([]byte, error) { return head, nil })
			if e != nil {
				return nil, e
			}
			checker.purposeChanged = headValue != checker.purpose
		} else {
			checker.purposeChanged = checker.purpose
		}
	}
	// манифесты - и с диска, и из индекса: удалённый только с диска манифест остаётся в коммите и проверяется
	paths, err := filepath.Glob(filepath.Join(codex, "manifest", "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("list manifests: %w", err)
	}
	paths = mergePaths(paths, commit.indexedUnder(filepath.Join(codex, "manifest"), ".yaml"))
	if err = commit.prefetch(paths); err != nil {
		return nil, err
	}
	referenced := map[string]bool{}
	var warnings, problems []string
	for _, path := range paths {
		w, p, err := checkStagedManifest(commit, codex, path, referenced, checker)
		if err != nil {
			return nil, err
		}
		warnings = append(warnings, w...)
		problems = append(problems, p...)
	}
	orphans, recordWarnings, err := stagedOrphans(commit, codex, referenced)
	if err != nil {
		return nil, err
	}
	return finishCheck(append(warnings, recordWarnings...), append(problems, orphans...))
}

// checkStagedManifest - проверка одного манифеста коммита; части каждого читаемого манифеста индекса
// помечаются в referenced: манифест, удалённый в коммите, сюда не попадает, и его фрагменты станут сиротами.
func checkStagedManifest(commit *stagedPaths, codex, path string, referenced map[string]bool, checker *toolChecker) (warnings, problems []string, err error) {
	raw, rerr := commit.read(path)
	if rerr != nil {
		if commit.has(path) {
			problems = append(problems, commit.treeProblem(rerr.Error(), path))
		}
		return nil, problems, nil // манифест вне коммита и вне индекса - чужая работа на диске
	}
	m, perr := parseManifest(path, raw)
	if perr != nil {
		if commit.has(path) {
			problems = append(problems, invalidManifestProblem(path, perr))
		}
		return nil, problems, nil // битый манифест HEAD чинит его коммит
	}
	for _, id := range m.parts {
		referenced[id] = true
	}
	if linkProblems := stagedLinkProblems(commit, m.links); len(linkProblems) > 0 {
		problems = append(problems, linkProblems...)
	}
	touched := commit.touches(m, path, codex)
	if !touched && checker.purposeChanged {
		touched, err = stagedHasPurpose(commit, codex, m)
		if err != nil {
			return warnings, append(problems, err.Error()), nil
		}
	}
	if !touched {
		return warnings, problems, nil // фрагмент в коммите делает свой манифест задетым, так что сироту это не скроет
	}
	fragPaths := make([]string, 0, len(m.parts)+1)
	for _, id := range m.parts {
		fragPaths = append(fragPaths, filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
	}
	if err = commit.prefetch(append(fragPaths, m.out)); err != nil {
		return nil, nil, err
	}
	// фрагменты, прочитанные из индекса, - для подсказки о непроиндексированной правке
	indexed := map[string][]byte{}
	read := func(f string) ([]byte, error) {
		b, err := commit.read(f)
		if err == nil {
			if commit.rel(f) != "" {
				indexed[f] = b
			}
			w, p := checker.checkWithWarnings(f, b)
			warnings = append(warnings, w...)
			problems = append(problems, p...)
		}
		return b, err
	}
	body, missing, draftWarnings := checkedManifestBody(codex, m, referenced, read, checker.purpose)
	warnings = append(warnings, draftWarnings...)
	problems = append(problems, missing...)

	if m.header {
		body = generatedHeader(path, body)
	}
	staged, diskDiff, oerr := commit.output(m.out)
	if oerr != nil {
		return warnings, append(problems, commit.labelProblem(oerr.Error(), m.out)), nil
	}
	if diskDiff {
		problems = append(problems, commit.labelProblem(m.out+": index and disk differ — stage the whole file (git add) or restore it", m.out))
	}
	if !outputMatchesGenerated(staged, body, m.header, path) {
		problems = append(problems, commit.labelProblem(builtDiffers(m.out, codex, m, path, body, staged), m.out))
		hints := unstagedFragmentHints(commit, fragPaths, indexed, m.out)
		for i := range hints {
			hints[i] = commit.labelProblem(hints[i], m.out)
		}
		problems = append(problems, hints...)
	}
	return warnings, problems, nil
}

func stagedLinkProblems(commit *stagedPaths, links []outputLink) []string {
	var problems []string
	for _, link := range links {
		if !commit.has(link.path) {
			continue
		}
		rel := commit.rel(link.path)
		current := link
		current.path = commit.treePath(link.path)
		sha, exists := commit.index[rel]
		if !exists {
			problems = append(problems, linkMismatch(current, "removed from the index in this commit").Error())
			continue
		}
		if commit.modes[rel] != "120000" {
			problems = append(problems, stagedLinkIsRegularFile(current).Error())
			continue
		}
		if err := commit.prefetch([]string{link.path}); err != nil {
			problems = append(problems, err.Error())
			continue
		}
		target, exists := commit.blobs[sha]
		if !exists || string(target) != link.target {
			actual := ""
			if exists {
				actual = strconv.Quote(string(target))
			}
			problems = append(problems, linkMismatch(current, "has index target "+actual).Error())
		}
	}
	return problems
}

func stagedLinkIsRegularFile(link outputLink) error {
	return fmt.Errorf("%s: staged as a regular file, expected symlink to %q; restore with ln -sfn %q %q",
		link.path, link.target, link.target, link.path)
}

// unstagedFragmentHints - частая причина расхождения: фрагмент правлен и пересобран, но не добавлен в коммит.
func unstagedFragmentHints(commit *stagedPaths, fragPaths []string, indexed map[string][]byte, out string) []string {
	var hints []string
	for _, f := range fragPaths {
		staged, ok := indexed[f]
		if !ok {
			continue // вне репозитория или не прочитан из индекса: последнее checkedManifestBody уже назвал
		}
		diskPath := commit.treePath(f)
		disk, err := os.ReadFile(diskPath)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			hints = append(hints, diskPath+": fragment deleted on disk but not staged — git add it together with "+commit.treePath(out))
		case err != nil:
			hints = append(hints, fmt.Sprintf("%s: read fragment from disk: %v", diskPath, err))
		case !bytes.Equal(staged, disk):
			hints = append(hints, diskPath+": fragment changed on disk but not staged — git add it together with "+commit.treePath(out))
		}
	}
	return hints
}

// stagedOrphans - фрагменты индекса без манифеста; ошибка коммита, только если её сделал этот коммит: фрагмент
// в коммите или его часть убрал манифест, изменённый или удалённый в коммите (сирота в HEAD чужие коммиты не блокирует).
func stagedOrphans(commit *stagedPaths, codex string, referenced map[string]bool) ([]string, []string, error) {
	headParts, err := commit.headParts(filepath.Join(codex, "manifest"))
	if err != nil {
		return nil, nil, err
	}
	var problems, warnings []string
	for _, frag := range commit.indexedFragments(filepath.Join(codex, "frag")) {
		if filepath.Ext(frag.abs) != ".md" {
			continue
		}
		if !referenced[frag.id] && (commit.has(frag.abs) || headParts[frag.id]) {
			b, readErr := commit.read(frag.abs)
			if readErr != nil {
				return nil, nil, readErr
			}
			if fragmentIsRecord(b) {
				warnings = append(warnings, codeLinkWarnings(frag.abs, b)...)
				warnings = append(warnings, overrideWarnings(codex, frag.id, b, commit.read)...)
				continue
			}
			problems = append(problems, frag.abs+": fragment is not in any manifest")
		}
	}
	return problems, warnings, nil
}

func builtDiffers(out, codex string, m manifest, manifestPath string, want, actual []byte) string {
	codexArg, fileArg := strconv.Quote(codex), strconv.Quote(out)
	if owner, foreign := foreignOutputOwner(codex, actual); foreign {
		return fmt.Sprintf("%s: the file carries %s, so lex import and lex absorb refuse it — if this codex owns the file, run `lex build --force --codex %s %s` to rebuild it from the fragments (edits in the file are lost)", out, ForeignHeaderPhrase(owner), codexArg, fileArg)
	}
	fragmentsChanged := fragmentsChangedSinceBuild(codex, manifestPath, m, want)
	fileChanged := statusFileChanged(actual, manifestPath, m)
	if fragmentsChanged && !fileChanged {
		return fmt.Sprintf("%s: built output differs from its fragments — edit the fragment and run `lex build --codex %s`", out, codexArg)
	}
	if fragmentsChanged && fileChanged {
		sharedFlag, sharedHint, err := codexSharedForceHint(codex, m)
		if err != nil {
			return fmt.Sprintf("%s: %v", out, err)
		}
		return fmt.Sprintf("%s: built output differs from its fragments — run `lex absorb --force%s --codex %s %s` to keep the file, or `lex build --force --codex %s %s` to keep the fragments%s", out, sharedFlag, codexArg, fileArg, codexArg, fileArg, sharedHint)
	}
	return fmt.Sprintf("%s: built output differs from its fragments — edit the fragment and run `lex build --codex %s`, or keep this edit with `lex absorb --codex %s %s`", out, codexArg, codexArg, fileArg)
}

func finishCheck(warnings, problems []string) ([]string, error) {
	seen := make(map[string]bool, len(warnings))
	deduped := warnings[:0]
	for _, warning := range warnings {
		if isRuleBasisWarning(warning) {
			if seen[warning] {
				continue
			}
			seen[warning] = true
		}
		deduped = append(deduped, warning)
	}
	warnings = deduped
	sort.Strings(problems)
	sort.Strings(warnings)
	if len(problems) > 0 {
		return warnings, errors.New(strings.Join(problems, "\n"))
	}
	return warnings, nil
}

func checkedManifestBody(codex string, m manifest, referenced map[string]bool, read func(string) ([]byte, error), purposeEnabled bool) ([]byte, []string, []string) {
	var body []byte
	var problems, warnings []string
	for _, id := range m.parts {
		referenced[id] = true
		path := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
		raw, err := read(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: missing fragment: %v", path, err))
			continue
		}
		if fragmentIsDraft(raw) {
			warnings = append(warnings, path+": draft: true")
		}
		warnings = append(warnings, ruleFieldWarnings(path, raw)...)
		warnings = append(warnings, codeLinkWarnings(path, raw)...)
		warnings = append(warnings, overrideWarnings(codex, id, raw, read)...)
		if fragmentIsRecord(raw) {
			problems = append(problems, path+": record must not be in a manifest")
			continue
		}
		part, err := fragmentBody(raw)
		if err != nil {
			problems = append(problems, path+": "+err.Error())
			continue
		}
		part, warning, err := renderPurpose(path, raw, part, purposeEnabled)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		if warning != "" {
			warnings = append(warnings, warning)
		}
		body = append(body, part...)
	}
	return body, problems, warnings
}

// stagedPaths - пути, входящие в коммит текущего репозитория (git diff --cached), от его корня.
type stagedPaths struct {
	root     string
	mainRoot string
	paths    map[string]bool
	index    map[string]string // путь от корня -> sha блоба в индексе
	modes    map[string]string
	blobs    map[string][]byte
}

type stagedFragment struct{ abs, id string }

func readStagedPaths() (*stagedPaths, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get current directory: %w", err)
	}
	rootBytes, err := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return nil, fmt.Errorf("git rev-parse current directory: %w", err)
	}
	root := resolvePath(strings.TrimSpace(string(rootBytes)))
	gitDir, err := exec.Command("git", "-C", root, "rev-parse", "--path-format=absolute", "--git-dir").Output()
	if err != nil {
		return nil, fmt.Errorf("git rev-parse git-dir in %s: %w", root, err)
	}
	commonDir, err := exec.Command("git", "-C", root, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return nil, fmt.Errorf("git rev-parse git-common-dir in %s: %w", root, err)
	}
	var mainRoot string
	if resolvePath(strings.TrimSpace(string(gitDir))) != resolvePath(strings.TrimSpace(string(commonDir))) {
		porcelain, e := exec.Command("git", "-C", root, "worktree", "list", "--porcelain").Output()
		if e != nil {
			return nil, fmt.Errorf("git worktree list in %s: %w", root, e)
		}
		for line := range strings.SplitSeq(string(porcelain), "\n") {
			if after, ok := strings.CutPrefix(line, "worktree "); ok {
				mainRoot = resolvePath(after)
				break
			}
		}
		if mainRoot == "" {
			return nil, fmt.Errorf("git worktree list in %s: no worktree found", root)
		}
	}
	// --no-renames: при переименовании в коммит входят оба пути, старый тоже
	names, err := exec.Command("git", "-C", root, "diff", "--cached", "--no-renames", "--name-only", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --cached in %s: %w", root, err)
	}
	set := map[string]bool{}
	for name := range strings.SplitSeq(string(names), "\x00") {
		if name != "" {
			set[name] = true
		}
	}
	listing, err := exec.Command("git", "-C", root, "ls-files", "-s", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files in %s: %w", root, err)
	}
	index := map[string]string{}
	modes := map[string]string{}
	for entry := range strings.SplitSeq(string(listing), "\x00") {
		meta, name, ok := strings.Cut(entry, "\t")
		if !ok {
			continue
		}
		if f := strings.Fields(meta); len(f) == 3 && f[2] == "0" {
			index[name] = f[1]
			modes[name] = f[0]
		}
	}
	return &stagedPaths{root: root, mainRoot: mainRoot, paths: set, index: index, modes: modes, blobs: map[string][]byte{}}, nil
}

// prefetch - блобы индекса для путей одним вызовом git cat-file --batch.
func (s *stagedPaths) prefetch(paths []string) error {
	var shas []string
	seen := map[string]bool{}
	for _, p := range paths {
		rel := s.rel(p)
		sha, ok := s.index[rel]
		if rel == "" || !ok || seen[sha] {
			continue
		}
		if _, cached := s.blobs[sha]; cached {
			continue
		}
		seen[sha] = true
		shas = append(shas, sha)
	}
	if len(shas) == 0 {
		return nil
	}
	cmd := exec.Command("git", "-C", s.root, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(strings.Join(shas, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git cat-file --batch in %s: %w", s.root, err)
	}
	for _, sha := range shas {
		header, rest, ok := bytes.Cut(out, []byte("\n"))
		if !ok {
			return fmt.Errorf("git cat-file --batch: truncated output at %s", sha)
		}
		f := strings.Fields(string(header))
		if len(f) != 3 || f[0] != sha {
			return fmt.Errorf("git cat-file --batch: unexpected header %q for %s", header, sha)
		}
		size, err := strconv.Atoi(f[2])
		if err != nil || size+1 > len(rest) {
			return fmt.Errorf("git cat-file --batch: bad size in %q", header)
		}
		s.blobs[sha] = rest[:size]
		out = rest[size+1:]
	}
	return nil
}

// indexedUnder - пути индекса внутри каталога dir с расширением ext (абсолютные).
func (s *stagedPaths) indexedUnder(dir, ext string) []string {
	base := s.rel(dir)
	if base == "" {
		return nil
	}
	var out []string
	for rel := range s.index {
		if strings.HasPrefix(rel, base+"/") && !strings.Contains(strings.TrimPrefix(rel, base+"/"), "/") && strings.HasSuffix(rel, ext) {
			out = append(out, filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(rel, base+"/"))))
		}
	}
	sort.Strings(out)
	return out
}

// mergePaths - объединение путей без повторов (по раскрытому пути), в порядке сортировки.
func mergePaths(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range append(append([]string{}, a...), b...) {
		k := resolvePath(p)
		if !seen[k] {
			seen[k] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// canonicalCodex - путь кодекса абсолютный и с раскрытыми симлинками на входе каждой команды: шапка собранного
// файла и проверка коммита не зависят от формы --codex (относительный, через симлинк).
func canonicalCodex(codex string, allowCreate bool) (string, error) {
	// A codex named by a generated header can start with ~/; callers such as
	// the graft gate pass that path without a shell.
	if codex == "~" || strings.HasPrefix(codex, "~/") {
		codex = expandHeaderCodexPath(codex)
	}
	abs, err := filepath.Abs(codex)
	if err != nil {
		return "", fmt.Errorf("%s: resolve codex path: %w", codex, err)
	}
	abs = resolvePath(abs)
	info, err := os.Stat(abs)
	if os.IsNotExist(err) && allowCreate {
		return abs, nil
	}
	if err != nil {
		return "", fmt.Errorf("%s: inspect codex: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s: codex is not a directory", abs)
	}
	if allowCreate {
		return abs, nil
	}
	manifest := filepath.Join(abs, "manifest")
	info, err = os.Stat(manifest)
	if err != nil {
		return "", fmt.Errorf("%s: codex manifest directory: %w", manifest, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s: codex manifest is not a directory", manifest)
	}
	return abs, nil
}

// resolvePath - абсолютный путь abs с раскрытыми симлинками; несуществующий хвост (удалённый файл) остаётся как есть.
func resolvePath(abs string) string {
	abs = filepath.Clean(abs)
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	dir, base := filepath.Split(abs)
	dir = filepath.Clean(dir)
	if dir == abs {
		return abs
	}
	return filepath.Join(resolvePath(dir), base)
}

// rel - путь от корня репозитория или "", если путь вне репозитория или во вложенном репозитории.
func (s *stagedPaths) rel(path string) string {
	abs := filepath.Clean(path)
	if !filepath.IsAbs(abs) {
		return ""
	}
	abs = filepath.Join(resolvePath(filepath.Dir(abs)), filepath.Base(abs))
	base := s.root
	rel, inside := strings.CutPrefix(abs, base+string(filepath.Separator))
	if !inside && s.mainRoot != "" {
		base = s.mainRoot
		rel, inside = strings.CutPrefix(abs, base+string(filepath.Separator))
	}
	if !inside {
		return ""
	}
	// вложенный репозиторий внутри work-tree содержит отдельный проект: его файлы не в этом индексе
	for dir := filepath.Dir(abs); dir != base && strings.HasPrefix(dir, base+string(filepath.Separator)); dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return ""
		}
	}
	return filepath.ToSlash(rel)
}

// treePath maps a path represented by the main checkout into this worktree for disk reads.
func (s *stagedPaths) treePath(path string) string {
	rel := s.rel(path)
	if rel != "" {
		return filepath.Join(s.root, filepath.FromSlash(rel))
	}
	return path
}

func (s *stagedPaths) labelProblem(problem, out string) string {
	if s.treePath(out) == out {
		return problem
	}
	prefix := out + ": "
	if after, ok := strings.CutPrefix(problem, prefix); ok {
		return s.treePath(out) + " (built by the codex as " + out + "): " + after
	}
	return problem
}

// treeProblem names the worktree copy of a codex file at the start of a problem.
func (s *stagedPaths) treeProblem(problem, path string) string {
	if after, ok := strings.CutPrefix(problem, path+": "); ok {
		return s.treePath(path) + ": " + after
	}
	return problem
}

func (s *stagedPaths) has(path string) bool {
	rel := s.rel(path)
	return rel != "" && s.paths[rel]
}

// read - версия файла из индекса для путей репозитория, иначе с диска.
func (s *stagedPaths) read(path string) ([]byte, error) {
	rel := s.rel(path)
	if rel == "" {
		return os.ReadFile(s.treePath(path))
	}
	sha, ok := s.index[rel]
	if !ok {
		if s.paths[rel] {
			return nil, fmt.Errorf("%s: removed from the index in this commit — restore it or remove it from the codex too", path)
		}
		return nil, fmt.Errorf("%s: not in the index — git add it", path)
	}
	if err := s.prefetch([]string{path}); err != nil {
		return nil, err
	}
	blob, ok := s.blobs[sha]
	if !ok {
		return nil, fmt.Errorf("%s: blob %s not read from the index", path, sha)
	}
	return blob, nil
}

// output - собранный файл из индекса и признак расхождения индекса с диском.
func (s *stagedPaths) output(path string) ([]byte, bool, error) {
	if s.rel(path) == "" {
		data, err := os.ReadFile(s.treePath(path))
		return data, false, err
	}
	index, err := s.read(path)
	if err != nil {
		return nil, false, err
	}
	disk, err := os.ReadFile(s.treePath(path))
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	return index, !bytes.Equal(index, disk), nil
}

func (s *stagedPaths) touches(m manifest, manifestPath, codex string) bool {
	if s.has(m.out) || s.has(manifestPath) {
		return true
	}
	for _, id := range m.parts {
		if s.has(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")) {
			return true
		}
	}
	return false
}

// headParts - части манифестов каталога dir в HEAD, если этот манифест изменён или удалён в коммите.
// Манифеста нет в HEAD (новый, первый коммит) - частей нет; битый манифест HEAD - тоже: его части
// неизвестны, а починка битого манифеста - его коммит, проверенный выше как манифест коммита.
func (s *stagedPaths) headParts(dir string) (map[string]bool, error) {
	base := s.rel(dir)
	parts := map[string]bool{}
	if base == "" {
		return parts, nil
	}
	var specs []string
	for rel := range s.paths {
		if strings.HasPrefix(rel, base+"/") && strings.HasSuffix(rel, ".yaml") {
			specs = append(specs, "HEAD:"+rel)
		}
	}
	if len(specs) == 0 {
		return parts, nil
	}
	sort.Strings(specs)
	cmd := exec.Command("git", "-C", s.root, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(strings.Join(specs, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git cat-file --batch HEAD manifests in %s: %w", s.root, err)
	}
	for _, spec := range specs {
		header, rest, ok := bytes.Cut(out, []byte("\n"))
		if !ok {
			return nil, fmt.Errorf("git cat-file --batch: truncated output at %s", spec)
		}
		if string(header) == spec+" missing" {
			out = rest
			continue
		}
		f := strings.Fields(string(header))
		if len(f) != 3 || f[1] != "blob" {
			return nil, fmt.Errorf("git cat-file --batch: unexpected header %q for %s", header, spec)
		}
		size, err := strconv.Atoi(f[2])
		if err != nil || size+1 > len(rest) {
			return nil, fmt.Errorf("git cat-file --batch: bad size in %q", header)
		}
		manifestPath := filepath.Join(s.root, filepath.FromSlash(strings.TrimPrefix(spec, "HEAD:")))
		if m, perr := parseManifest(manifestPath, rest[:size]); perr == nil {
			for _, id := range m.parts {
				parts[id] = true
			}
		} else {
			return nil, fmt.Errorf("HEAD manifest %s: %w", manifestPath, perr)
		}
		out = rest[size+1:]
	}
	return parts, nil
}

// indexedFragments - пути индекса внутри каталога dir (фрагменты кодекса) с id фрагмента.
func (s *stagedPaths) indexedFragments(dir string) []stagedFragment {
	base := s.rel(dir)
	if base == "" {
		return nil
	}
	var out []stagedFragment
	for rel := range s.index {
		if after, ok := strings.CutPrefix(rel, base+"/"); ok {
			id := strings.TrimSuffix(after, ".md")
			out = append(out, stagedFragment{abs: filepath.Join(s.root, filepath.FromSlash(rel)), id: id})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].abs < out[j].abs })
	return out
}

func appendOrphanProblem(codex string, referenced map[string]bool) error {
	root := filepath.Join(codex, "frag")
	return filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		id := strings.TrimSuffix(filepath.ToSlash(rel), ".md")
		if !referenced[id] {
			b, err := os.ReadFile(path)
			if err == nil && fragmentIsRecord(b) {
				return nil
			}
			return fmt.Errorf("%s: fragment is not in any manifest", path)
		}
		return nil
	})
}

// StatusEntry describes one output file that differs from the codex.
type StatusEntry struct {
	Manifest, Output string
	// Command settles the difference. When KeepFragments is set as well,
	// Command keeps the file's text and drops the fragment edits.
	Command string
	// KeepFragments is set when the fragments changed after the last build too:
	// the command that keeps them and drops the file's edits.
	KeepFragments string
	// SharedHint is printed after the status recommendation's full sentence.
	SharedHint string
	// ConflictSummary describes which versions are known to differ.
	ConflictSummary string
	// ProblemFile distinguishes an unreadable output from fragment errors.
	ProblemFile bool
	// Problem says why the file could not be compared; Command is empty then.
	Problem string
	// ForeignCodex names the codex whose lex header the file carries; Command then rebuilds the file from this codex.
	ForeignCodex string
}

// statusFragmentProblem names the path of a failed file operation once.
func statusFragmentProblem(err error) string {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Path + ": " + pathErr.Err.Error()
	}
	return err.Error()
}

// statusFileProblem says why the output cannot be read without its path: the status line names the output already.
func statusFileProblem(err error) string {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}
	return err.Error()
}

func Status(codex string) ([]StatusEntry, error) { return StatusWithHome(codex, "") }
func StatusWithHome(codex, home string) ([]StatusEntry, error) {
	codex, err := canonicalCodex(codex, false)
	if err != nil {
		return nil, err
	}
	if home == "" {
		current, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("find current user's home: %w", err)
		}
		home = current
	}
	paths, err := filepath.Glob(filepath.Join(codex, "manifest", "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("list manifests: %w", err)
	}
	sort.Strings(paths)
	var changed []StatusEntry
	for _, p := range paths {
		m, err := readManifest(p)
		if err != nil {
			return nil, err
		}
		entry := StatusEntry{Manifest: p, Output: m.out}
		actual, err := os.ReadFile(m.out)
		if err != nil {
			entry.Problem = statusFileProblem(err)
			entry.ProblemFile = true
			changed = append(changed, entry)
			continue
		}
		if !statusFileChanged(actual, p, m) {
			continue
		}
		want, err := manifestBody(codex, m)
		if err != nil {
			entry.Problem = statusFragmentProblem(err)
			changed = append(changed, entry)
			continue
		}
		if m.header {
			want = generatedHeader(p, want)
		}
		if outputMatchesGenerated(actual, want, m.header, p) {
			continue
		}
		if err := recommendStatusCommands(&entry, codex, home, p, m, actual, want); err != nil {
			return nil, err
		}
		changed = append(changed, entry)
	}
	return changed, nil
}

func statusFileChanged(actual []byte, manifestPath string, m manifest) bool {
	if m.builtHash != "" {
		return sha(actual) != m.builtHash
	}
	source := actual
	if m.header {
		source = withoutGeneratedHeader(actual, manifestPath)
	}
	return sha(source) != m.hash
}

func recommendStatusCommands(entry *StatusEntry, codex, home, manifestPath string, m manifest, actual, want []byte) error {
	assessment, refreshErr := assessRefresh(codex, manifestKey(manifestPath), manifestPath, m, actual)
	fragmentsChanged := fragmentsChangedSinceBuild(codex, manifestPath, m, want)
	rel, err := filepath.Rel(home, m.out)
	if err != nil {
		return fmt.Errorf("%s: resolve source path: %w", m.out, err)
	}
	target := strconv.Quote(codex) + " " + strconv.Quote(filepath.Join(home, rel))
	if owner, foreign := foreignOutputOwner(codex, actual); foreign {
		// lex import and lex absorb refuse such a file; only build --force rebuilds it.
		entry.ForeignCodex = owner
		entry.Command = "lex build --force --codex " + target
		return nil
	}
	entry.Command = "lex import --refresh --codex " + target
	if fragmentsChanged {
		entry.KeepFragments = "lex build --force --codex " + target
		sharedFlag, sharedHint, err := codexSharedForceHint(codex, m)
		if err != nil {
			return err
		}
		entry.Command = "lex absorb --force" + sharedFlag + " --codex " + target
		entry.SharedHint = sharedHint
		if m.builtHash == "" {
			entry.ConflictSummary = "the file differs from its recorded source and the fragments differ from the manifest"
		} else {
			entry.ConflictSummary = "the file and its fragments both changed since the last build"
		}
		return nil
	}
	if m.header || refreshErr != nil || assessment.noChange {
		entry.Command = "lex absorb --codex " + target
	}
	return nil
}

func split(data []byte) [][]byte {
	if len(data) == 0 {
		return nil
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	state := splitState{listIndent: -1}
	frontMatterLines := frontMatterLength(lines)
	for i := range frontMatterLines {
		state.current = append(state.current, lines[i])
	}
	if frontMatterLines > 0 {
		state.flush()
	}
	for _, line := range lines[frontMatterLines:] {
		state.accept(line)
	}
	state.flush()
	return state.units
}

type splitState struct {
	units       [][]byte
	current     [][]byte
	activeFence byte
	fenceSize   int
	fenceInList bool
	listIndent  int
}

func frontMatterLength(lines [][]byte) int {
	if len(lines) == 0 || cleanLine(lines[0]) != "---" {
		return 0
	}
	for i := 1; i < len(lines); i++ {
		if cleanLine(lines[i]) == "---" {
			return i + 1
		}
	}
	return 0
}

func cleanLine(line []byte) string {
	return strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r")
}

func (s *splitState) flush() {
	if len(s.current) == 0 {
		return
	}
	s.units = append(s.units, bytes.Join(s.current, nil))
	s.current = nil
}

func (s *splitState) accept(line []byte) {
	text := cleanLine(line)
	if s.acceptFence(line, text) {
		return
	}
	if strings.TrimSpace(text) == "" {
		s.acceptBlank(line)
		return
	}
	if heading(text) {
		s.flush()
		s.current = append(s.current, line)
		s.flush()
		s.listIndent = -1
		return
	}
	s.acceptListOrParagraph(line, text)
}

func (s *splitState) acceptFence(line []byte, text string) bool {
	if s.activeFence != 0 {
		s.current = append(s.current, line)
		if closesFence(text, s.activeFence, s.fenceSize) {
			s.activeFence = 0
			if !s.fenceInList {
				s.flush()
				s.listIndent = -1
			}
		}
		return true
	}
	marker, size, indent, ok := opensFence(text)
	if !ok {
		return false
	}
	s.fenceInList = s.listIndent >= 0 && indent > 0
	if !s.fenceInList {
		s.flush()
	}
	s.current = append(s.current, line)
	s.activeFence, s.fenceSize = marker, size
	return true
}

func (s *splitState) acceptBlank(line []byte) {
	if len(s.current) > 0 {
		s.current = append(s.current, line)
		s.flush()
		return
	}
	if len(s.units) > 0 {
		s.units[len(s.units)-1] = append(s.units[len(s.units)-1], line...)
		return
	}
	s.current = append(s.current, line)
}

func (s *splitState) acceptListOrParagraph(line []byte, text string) {
	indent := len(text) - len(strings.TrimLeft(text, " \t"))
	isItem := listLine(text)
	if isItem && indent == 0 {
		s.flush()
		s.current = append(s.current, line)
		s.listIndent = indent
		return
	}
	if indent > 0 && s.listIndent >= 0 {
		s.current = append(s.current, line)
		return
	}
	if isItem {
		s.flush()
		s.current = append(s.current, line)
		s.listIndent = indent
		return
	}
	s.listIndent = -1
	s.current = append(s.current, line)
}

func opensFence(line string) (byte, int, int, bool) {
	indent := len(line) - len(strings.TrimLeft(line, " "))
	if indent > 3 {
		return 0, 0, indent, false
	}
	s := line[indent:]
	if len(s) < 3 || s[0] != '`' && s[0] != '~' {
		return 0, 0, indent, false
	}
	i := 0
	for i < len(s) && s[i] == s[0] {
		i++
	}
	if i < 3 || s[0] == '`' && strings.Contains(s[i:], "`") {
		return 0, 0, indent, false
	}
	return s[0], i, indent, true
}

func closesFence(line string, marker byte, size int) bool {
	indent := len(line) - len(strings.TrimLeft(line, " "))
	if indent > 3 {
		return false
	}
	s := line[indent:]
	i := 0
	for i < len(s) && s[i] == marker {
		i++
	}
	return i >= size && strings.TrimSpace(s[i:]) == ""
}

var headingRE = regexp.MustCompile(`^#{1,6}[ \t]+`)
var itemRE = regexp.MustCompile(`^(?:[-*][ \t]+|[0-9]+\.[ \t]+)`)

func heading(s string) bool  { return headingRE.MatchString(s) }
func listLine(s string) bool { return itemRE.MatchString(strings.TrimLeft(s, " \t")) }
func slug(b []byte) string {
	s := strings.TrimSpace(string(b))
	s = strings.TrimLeft(s, "#*-0123456789. \t")
	out := strings.Builder{}
	dash := false
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			out.WriteRune(r)
			dash = false
		} else if !dash && out.Len() > 0 {
			out.WriteByte('-')
			dash = true
		}
	}
	value := strings.Trim(out.String(), "-")
	if len(value) > 48 {
		runes := []rune(value)
		for len(string(runes)) > 48 {
			runes = runes[:len(runes)-1]
		}
		value = strings.TrimRight(string(runes), "-")
	}
	return value
}
func sha(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func fragmentHeader(raw []byte) []byte {
	if !bytes.HasPrefix(raw, []byte("---\n")) {
		return nil
	}
	end := bytes.Index(raw[4:], []byte("---\n"))
	if end < 0 {
		return nil
	}
	return append([]byte(nil), raw[:4+end+4]...)
}
func replaceHeaderID(header []byte, id string) []byte {
	lines := bytes.SplitAfter(header, []byte("\n"))
	for i, line := range lines {
		if bytes.HasPrefix(line, []byte("id: ")) {
			lines[i] = []byte("id: " + id + "\n")
			break
		}
	}
	return bytes.Join(lines, nil)
}
func fragmentBody(b []byte) ([]byte, error) {
	if !bytes.HasPrefix(b, []byte("---\n")) {
		return nil, errors.New("invalid fragment header: expected opening ---")
	}
	lines := bytes.SplitAfter(b[4:], []byte("\n"))
	want := []string{"id:", "цель:", "основание:", "снять_когда:"}
	for i, key := range want {
		if i >= len(lines) {
			return nil, fmt.Errorf("invalid fragment header: missing %s", key)
		}
		line := strings.TrimSuffix(string(lines[i]), "\n")
		if !strings.HasPrefix(line, key+" ") && line != key {
			return nil, fmt.Errorf("invalid fragment header: expected %s", key)
		}
	}
	if len(lines) <= len(want) {
		return nil, errors.New("invalid fragment header: missing closing ---")
	}
	closeAt := len(want)
	if closeAt < len(lines) && strings.TrimSpace(string(lines[closeAt])) == "тип: правило" {
		closeAt++
	}
	for closeAt < len(lines) {
		line := strings.TrimSpace(string(lines[closeAt]))
		if optionalFragmentField(line) {
			closeAt++
			continue
		}
		break
	}
	if closeAt < len(lines) && strings.TrimSpace(string(lines[closeAt])) == "draft: true" {
		closeAt++
	}
	if closeAt >= len(lines) || string(lines[closeAt]) != "---\n" {
		return nil, errors.New("invalid fragment header: expected closing ---")
	}
	consumed := 4
	for _, line := range lines[:closeAt+1] {
		consumed += len(line)
	}
	return b[consumed:], nil
}

func optionalFragmentField(line string) bool {
	for _, prefix := range []string{"код: ", "код_sha256: ", "текст_sha256: ", "переопределяет: ", "переопределяет_sha256: ", "переопределяет_обрыв: "} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return line == "запись: true"
}

func ruleFieldWarnings(path string, raw []byte) []string {
	values := fragmentHeaderValues(raw)
	isRule := len(values["тип"]) > 0 && values["тип"][len(values["тип"])-1] == "правило"
	for _, field := range []string{"цель", "основание", "снять_когда"} {
		if len(values[field]) > 0 && values[field][len(values[field])-1] != "" {
			isRule = true
		}
	}
	var warnings []string
	if isRule {
		for _, field := range []string{"основание", "снять_когда"} {
			if len(values[field]) == 0 || values[field][len(values[field])-1] == "" {
				warnings = append(warnings, fmt.Sprintf("%s: rule has empty %s", path, field))
			}
		}
	}
	header := fragmentHeader(raw)
	if len(header) > 0 && hasUserQuoteInText(raw[len(header):]) {
		warnings = append(warnings, path+textQuoteWarning)
	}
	return warnings
}

// The three rule warnings of lex check, as they follow the fragment path.
const (
	emptyBasisWarning   = ": rule has empty основание"
	emptyRemovalWarning = ": rule has empty снять_когда"
	textQuoteWarning    = ": цитата пользователя в тексте, место ей — основание"
)

func isRuleBasisWarning(warning string) bool {
	return strings.Contains(warning, emptyBasisWarning) ||
		strings.Contains(warning, emptyRemovalWarning) ||
		strings.Contains(warning, textQuoteWarning)
}

// RuleWarningSummary returns the closing line of lex check: how many warnings of each rule kind there are.
func RuleWarningSummary(warnings []string) string {
	basis, removal, quote := 0, 0, 0
	for _, warning := range warnings {
		switch {
		case strings.Contains(warning, emptyBasisWarning):
			basis++
		case strings.Contains(warning, emptyRemovalWarning):
			removal++
		case strings.Contains(warning, textQuoteWarning):
			quote++
		}
	}
	return fmt.Sprintf("правил без основания: %d, без условия снятия: %d; фрагментов с цитатой пользователя в тексте: %d", basis, removal, quote)
}
func parseManifestLink(line string, m *manifest) (bool, error) {
	if strings.TrimSpace(line) == "" {
		return true, nil
	}
	if after, ok := strings.CutPrefix(line, "  - path: "); ok {
		value, err := strconv.Unquote(after)
		if err != nil {
			return false, fmt.Errorf("decode symlink path: %w", err)
		}
		m.links = append(m.links, outputLink{path: value, targetSet: false})
		return true, nil
	}
	if after, ok := strings.CutPrefix(line, "    target: "); ok && len(m.links) > 0 {
		if m.links[len(m.links)-1].targetSet {
			return false, fmt.Errorf("duplicate symlink target for %s", m.links[len(m.links)-1].path)
		}
		value, err := strconv.Unquote(after)
		if err != nil {
			return false, fmt.Errorf("decode symlink target: %w", err)
		}
		m.links[len(m.links)-1].target = value
		m.links[len(m.links)-1].targetSet = true
		return true, nil
	}
	return false, nil
}

func readManifest(path string) (manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return manifest{}, &manifestError{path: path, err: fmt.Errorf("read manifest: %w", err)}
	}
	return parseManifest(path, b)
}

func invalidManifestProblem(path string, err error) string {
	var manifestErr *manifestError
	if errors.As(err, &manifestErr) {
		return manifestErr.path + ": invalid manifest: " + manifestErr.err.Error()
	}
	return path + ": invalid manifest: " + err.Error()
}

func parseManifest(path string, b []byte) (manifest, error) {
	m := manifest{fragmentHashes: map[string]string{}}
	section := ""
	for line := range strings.SplitSeq(string(b), "\n") {
		next, err := parseManifestLine(line, section, &m)
		if err != nil {
			return m, &manifestError{path: path, err: err}
		}
		section = next
	}
	if m.out == "" || m.hash == "" {
		return m, &manifestError{path: path, err: errors.New("manifest requires out and source_sha256")}
	}
	for _, link := range m.links {
		if !filepath.IsAbs(link.path) || link.target == "" || !link.targetSet {
			return m, &manifestError{path: path, err: errors.New("manifest link requires an absolute path and target")}
		}
	}
	return m, nil
}

func parseManifestLine(line, section string, m *manifest) (string, error) {
	if strings.HasPrefix(line, "links:") && line != "links:" {
		return section, fmt.Errorf("invalid links section %q", line)
	}
	if line == "parts:" || line == "fragment_sha256:" || line == "links:" {
		return strings.TrimSuffix(line, ":"), nil
	}
	switch section {
	case "parts":
		return section, parseManifestPart(line, m)
	case "links":
		handled, err := parseManifestLink(line, m)
		if err != nil {
			return section, err
		}
		if !handled {
			return section, fmt.Errorf("invalid links entry %q", line)
		}
	case "fragment_sha256":
		return section, parseManifestHash(line, m)
	default:
		return section, parseManifestFields(line, m)
	}
	return section, nil
}

func parseManifestPart(line string, m *manifest) error {
	after, ok := strings.CutPrefix(line, "  - ")
	if !ok {
		return nil
	}
	id, err := strconv.Unquote(strings.TrimSpace(after))
	if err != nil {
		return fmt.Errorf("decode part id: %w", err)
	}
	m.parts = append(m.parts, id)
	return nil
}

func parseManifestHash(line string, m *manifest) error {
	if !strings.HasPrefix(line, "  ") {
		return nil
	}
	key, value, ok := strings.Cut(strings.TrimLeft(line, " "), ": ")
	if !ok {
		return fmt.Errorf("invalid fragment_sha256 entry %q", line)
	}
	id, err := strconv.Unquote(key)
	if err != nil {
		return fmt.Errorf("decode fragment hash id: %w", err)
	}
	if value != "" {
		m.fragmentHashes[id] = value
	}
	return nil
}

func parseManifestFields(line string, m *manifest) error {
	if after, ok := strings.CutPrefix(line, "out: "); ok {
		value, err := strconv.Unquote(after)
		if err != nil {
			return fmt.Errorf("decode output path: %w", err)
		}
		m.out = value
	}
	if after, ok := strings.CutPrefix(line, "source_sha256: "); ok {
		m.hash = after
	}
	if after, ok := strings.CutPrefix(line, "built_sha256: "); ok {
		m.builtHash = after
	}
	if after, ok := strings.CutPrefix(line, "header: "); ok {
		m.header = after == "true"
	}
	return nil
}
func firstDiff(a, b []byte) (int, string, string) {
	aa := bytes.Split(a, []byte("\n"))
	bb := bytes.Split(b, []byte("\n"))
	n := max(len(bb), len(aa))
	for i := range n {
		var x, y []byte
		if i < len(aa) {
			x = aa[i]
		}
		if i < len(bb) {
			y = bb[i]
		}
		if !bytes.Equal(x, y) {
			return i + 1, string(x), string(y)
		}
	}
	return n, "", ""
}
func trunc(s string) string {
	if len([]rune(s)) > 120 {
		return string([]rune(s)[:120])
	}
	return s
}
