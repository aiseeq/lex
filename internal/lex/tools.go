package lex

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

type toolConfig struct {
	Tools []configuredTool `json:"tools"`
}
type configuredTool struct {
	Name        string   `json:"name"`
	Help        []string `json:"help"`
	Dirs        []string `json:"dirs"`
	TaskCommand []string `json:"task_command"`
}
type toolCommand struct {
	Path       []string `json:"path"`
	Flags      []string `json:"flags"`
	ValueFlags []string `json:"value_flags"`
	Rest       string   `json:"rest"`
}
type toolRegistry struct {
	GlobalFlags      []string      `json:"global_flags"`
	GlobalValueFlags []string      `json:"global_value_flags"`
	Commands         []toolCommand `json:"commands"`
	Tasks            []string      `json:"tasks"`
	TasksError       string        `json:"tasks_error"`
}
type toolChecker struct {
	tools          []loadedTool
	seen           map[string]bool
	purpose        bool
	purposeChanged bool
}
type loadedTool struct {
	config            configuredTool
	registry          toolRegistry
	tasksKnown        bool
	codex, configPath string
	loaded            bool
	loadErr           error
	registryWarning   bool
}

func loadToolChecker(codex string) (*toolChecker, error) {
	c := &toolChecker{seen: map[string]bool{}}
	path := filepath.Join(codex, "tools.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var config toolConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for _, tool := range config.Tools {
		if tool.Name == "" || len(tool.Help) == 0 || tool.Help[0] == "" {
			return nil, fmt.Errorf("%s: tool requires name and help command", path)
		}
		c.tools = append(c.tools, loadedTool{config: tool, codex: codex, configPath: path})
	}
	return c, nil
}

func (t *loadedTool) load() error {
	if t.loaded {
		return t.loadErr
	}
	t.loaded = true
	t.loadErr = t.loadRegistry()
	return t.loadErr
}

func (t *loadedTool) loadRegistry() error {
	codex, path, tool := t.codex, t.configPath, t.config
	dirs, err := toolDirs(codex, path, tool)
	if err != nil {
		return err
	}
	loaded := t
	seenDirs := map[string]bool{}
	for _, dir := range dirs {
		dir = resolvePath(dir)
		if seenDirs[dir] {
			continue
		}
		seenDirs[dir] = true
		cmd := exec.Command(tool.Help[0], tool.Help[1:]...)
		cmd.Dir = dir
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		output, err := cmd.Output()
		if err != nil {
			return withStderr(fmt.Errorf("%s: %s in %s: %w", path, strings.Join(tool.Help, " "), dir, err), stderr.Bytes())
		}
		var reg toolRegistry
		if err := json.Unmarshal(output, &reg); err != nil {
			return withStderr(fmt.Errorf("%s: %s in %s: invalid JSON: %w", path, strings.Join(tool.Help, " "), dir, err), stderr.Bytes())
		}
		if len(seenDirs) == 1 {
			loaded.registry = reg
			loaded.registry.Tasks = nil
		}
		loaded.registry.Tasks = append(loaded.registry.Tasks, reg.Tasks...)
		if reg.TasksError == "" {
			loaded.tasksKnown = true
		}
	}
	if len(tool.TaskCommand) > 0 && t.taskCommand() == nil {
		return fmt.Errorf("%s: %s: task_command %s not found in registry (%s)", path, tool.Name, strings.Join(tool.TaskCommand, " "), strings.Join(tool.Help, " "))
	}
	return nil
}

func toolDirs(codex, path string, tool configuredTool) ([]string, error) {
	var dirs []string
	if len(tool.Dirs) == 0 {
		dirs = append(dirs, codex)
	}
	for _, pattern := range tool.Dirs {
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(codex, pattern)
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid directory glob: %w", path, err)
		}
		for _, dir := range matches {
			info, err := os.Stat(dir)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", dir, err)
			}
			if info.IsDir() {
				dirs = append(dirs, dir)
			}
		}
	}
	if len(dirs) == 0 {
		return nil, fmt.Errorf("%s: %s: no directories match dirs", path, tool.Name)
	}
	return dirs, nil
}

func (c *toolChecker) checkDiskFragmentsWithWarnings(codex string, references ...map[string]bool) (warnings, problems []string) {
	if len(c.tools) == 0 {
		return nil, nil
	}
	root := filepath.Join(codex, "frag")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == root {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		purpose := c.purpose
		if len(references) > 0 {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			id := strings.TrimSuffix(filepath.ToSlash(rel), ".md")
			c.purpose = purpose && references[0][id]
		}
		w, p := c.checkWithWarnings(path, raw)
		warnings = append(warnings, w...)
		problems = append(problems, p...)
		c.purpose = purpose
		return nil
	})
	if err != nil {
		problems = append(problems, err.Error())
	}
	return warnings, problems
}

func (c *toolChecker) checkWithWarnings(path string, raw []byte) (warnings, problems []string) {
	if c.seen[path] {
		return nil, nil
	}
	c.seen[path] = true
	body, err := fragmentBody(raw)
	if err != nil {
		return nil, []string{path + ": " + err.Error()}
	}
	body, _, err = renderPurpose(path, raw, body, c.purpose)
	if err != nil {
		return nil, []string{err.Error()}
	}
	for _, code := range codeSections(string(body)) {
		for _, words := range commandWords(code) {
			if len(words) == 0 {
				continue
			}
			for i := range c.tools {
				tool := &c.tools[i]
				if words[0] != tool.config.Name {
					continue
				}
				mention := strings.Join(words, " ")
				if !tool.loadRegistryAndWarn(&warnings) {
					continue
				}
				if problem := tool.problem(words); problem != "" {
					warning := fmt.Sprintf("%s: `%s`: %s (%s)", path, mention, problem, strings.Join(tool.config.Help, " "))
					warnings = append(warnings, warning)
				}
			}
		}
	}
	return warnings, problems
}

// withStderr appends what the help command wrote to stderr, when it wrote anything.
func withStderr(err error, stderr []byte) error {
	if text := strings.TrimSpace(string(stderr)); text != "" {
		return fmt.Errorf("%w: %s", err, text)
	}
	return err
}

func (t *loadedTool) loadRegistryAndWarn(warnings *[]string) bool {
	err := t.load()
	if err == nil {
		return true
	}
	if !t.registryWarning {
		*warnings = append(*warnings, fmt.Sprintf("tool %s registry: %v", t.config.Name, err))
		t.registryWarning = true
	}
	return false
}

var inlineCode = regexp.MustCompile("`+[^`\\n]+`+")

var heredocStart = regexp.MustCompile(`<<(-?)[ \t]*['"\\]?([A-Za-z_][A-Za-z_0-9]*)['"]?`)

type heredocDelimiter struct {
	word      string
	stripTabs bool
}

func fenceMarker(trimmed string) byte {
	if strings.HasPrefix(trimmed, "```") {
		return '`'
	}
	if strings.HasPrefix(trimmed, "~~~") {
		return '~'
	}
	return 0
}

func heredocSubstitutionStart(quote byte, line string) bool {
	return quote == '"' && strings.HasPrefix(line, "$(")
}

func heredocSubstitutionEnd(quote, c byte, active bool) bool {
	return active && c == ')' && quote == 0
}

func findHeredocs(line string) []heredocDelimiter {
	var out []heredocDelimiter
	var quote byte
	inSubstitution := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c == '\\' && quote != '\'' && i+1 < len(line) {
			i++
			continue
		}
		if heredocSubstitutionStart(quote, line[i:]) {
			quote = 0
			inSubstitution = true
			i++
			continue
		}
		if heredocSubstitutionEnd(quote, c, inSubstitution) {
			quote = '"'
			inSubstitution = false
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			break
		}
		if strings.HasPrefix(line[i:], "<<<") {
			i += 2
			continue
		}
		if c != '<' {
			continue
		}
		m := heredocStart.FindStringSubmatch(line[i:])
		if m == nil || !strings.HasPrefix(line[i:], m[0]) {
			continue
		}
		out = append(out, heredocDelimiter{word: m[2], stripTabs: m[1] == "-"})
		i += len(m[0]) - 1
	}
	return out
}

func codeSections(body string) []string {
	var sections []string
	var fence byte
	var continued string
	var indentation string
	var heredocs []heredocDelimiter
	allowed := false
	for line := range strings.SplitSeq(body, "\n") {
		trimmed := strings.TrimSpace(line)
		marker := fenceMarker(trimmed)
		if fence == 0 && marker != 0 {
			fence = marker
			indentation = line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			info := strings.TrimSpace(strings.TrimLeft(trimmed, string(marker)))
			lang, _, _ := strings.Cut(strings.ToLower(info), " ")
			allowed = lang == "" || slices.Contains([]string{"sh", "bash", "shell", "zsh", "console"}, lang)
			continue
		}
		if fence != 0 && marker == fence {
			if continued != "" && allowed {
				sections = append(sections, continued)
			}
			continued = ""
			heredocs = nil
			fence = 0
			continue
		}
		if fence != 0 {
			if !allowed {
				continue
			}
			if len(heredocs) > 0 {
				terminal := strings.TrimPrefix(line, indentation)
				if heredocs[0].stripTabs {
					terminal = strings.TrimLeft(terminal, "\t")
				}
				if terminal == heredocs[0].word {
					heredocs = heredocs[1:]
				}
				continue
			}
			line = continued + line
			if before, ok := strings.CutSuffix(line, "\\"); ok {
				continued = before + " "
				continue
			}
			sections = append(sections, line)
			continued = ""
			heredocs = append(heredocs, findHeredocs(line)...)
			continue
		}
		for _, span := range inlineCode.FindAllString(line, -1) {
			code := strings.Trim(span, "`")
			if strings.HasPrefix(trimmed, "|") {
				code = strings.ReplaceAll(code, `\|`, "|")
			}
			sections = append(sections, code)
		}
	}
	if continued != "" && allowed {
		sections = append(sections, continued)
	}
	return sections
}

func escapedDoubleQuote(runes []rune, i int, quote rune) bool {
	return quote == '"' && runes[i] == '\\' && i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '\\')
}

func wordSubstitutionStart(quote rune, runes []rune, i int) bool {
	return quote == '"' && runes[i] == '$' && i+1 < len(runes) && runes[i+1] == '('
}

func wordSubstitutionEnd(quote, r rune, active bool) bool {
	return active && quote == 0 && r == ')'
}

func startsBracket(r rune, word *strings.Builder) bool {
	return r == '[' && word.Len() == 0
}

func commandWords(code string) [][]string {
	var commands [][]string
	var words []string
	var word strings.Builder
	var quote rune
	inSubstitution := false
	skipNext := false
	bracket := false
	flushWord := func() {
		if word.Len() > 0 {
			words = append(words, word.String())
			word.Reset()
		}
	}
	flushCommand := func() {
		flushWord()
		words = stripCommandPrefix(words)
		if len(words) > 0 {
			commands = append(commands, words)
		}
		words = nil
	}
	runes := []rune(code)
	escaped := false
	for i := range runes {
		r := runes[i]
		if skipNext {
			skipNext = false
			continue
		}
		if wordSubstitutionStart(quote, runes, i) {
			flushCommand()
			quote = 0
			inSubstitution = true
			skipNext = true
			continue
		}
		if wordSubstitutionEnd(quote, r, inSubstitution) {
			flushCommand()
			quote = '"'
			inSubstitution = false
			continue
		}
		if escaped {
			word.WriteRune(r)
			escaped = false
			continue
		}
		if escapedDoubleQuote(runes, i, quote) {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
			continue
		}
		if bracket {
			word.WriteRune(r)
			if r == ']' {
				bracket = false
			}
			continue
		}
		switch {
		case startsBracket(r, &word):
			bracket = true
			word.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
		case unicode.IsSpace(r):
			flushWord()
		case r == '#' && word.Len() == 0:
			flushCommand()
			return commands
		case isCommandSeparator(r):
			flushCommand()
		default:
			word.WriteRune(r)
		}
	}
	flushCommand()
	return commands
}

func isCommandSeparator(r rune) bool {
	return r == '(' || r == ')' || r == ';' || r == '|' || r == '&'
}

func stripCommandPrefix(words []string) []string {
	for len(words) > 0 {
		switch {
		case words[0] == "sudo":
			words = skipSudo(words[1:])
		case words[0] == "$" || words[0] == "nohup" || words[0] == "time" || words[0] == "env" || assignment(words[0]):
			words = words[1:]
		case words[0] == "timeout":
			if len(words) < 2 {
				return nil
			}
			words = words[2:]
		default:
			return words
		}
	}
	return words
}

func skipSudo(words []string) []string {
	for len(words) > 0 && strings.HasPrefix(words[0], "-") {
		flag := words[0]
		words = words[1:]
		if flag == "--" {
			break
		}
		if slices.Contains([]string{"-u", "-g", "-C", "-h", "-p", "-D", "-r", "-t", "-U"}, flag) && len(words) > 0 {
			words = words[1:]
		}
	}
	return words
}

func assignment(word string) bool {
	name, _, ok := strings.Cut(word, "=")
	if !ok || name == "" {
		return false
	}
	for i, r := range name {
		if r != '_' && !unicode.IsLetter(r) && (i == 0 || !unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}

func placeholder(word string) bool {
	return strings.ContainsAny(word, "<>…{}$*") || word == "..." || strings.HasPrefix(word, "[") || strings.HasSuffix(word, ",")
}

func pathPlaceholder(word string) bool { return placeholder(word) || strings.Contains(word, "/") }
func flagName(word string) string      { name, _, _ := strings.Cut(word, "="); return name }
func skipFlag(words []string, i int, values []string) int {
	if slices.Contains(values, flagName(words[i])) && !strings.Contains(words[i], "=") && i+1 < len(words) {
		if placeholder(words[i+1]) {
			return len(words)
		}
		return i + 2
	}
	return i + 1
}

func (t loadedTool) globalFlags(words []string) (int, string) {
	reg := t.registry
	i := 1
	for i < len(words) {
		w := words[i]
		if placeholder(w) || w == "--" {
			return len(words), ""
		}
		if !strings.HasPrefix(w, "-") {
			break
		}
		flag := flagName(w)
		if !slices.Contains(reg.GlobalFlags, flag) {
			return i, fmt.Sprintf("%s has no flag %s", t.config.Name, flag)
		}
		i = skipFlag(words, i, reg.GlobalValueFlags)
	}
	return i, ""
}

func (t loadedTool) problem(words []string) string {
	i, problem := t.globalFlags(words)
	if problem != "" || i == len(words) {
		return problem
	}
	reg := t.registry
	start := i
	if pathPlaceholder(words[start]) {
		return ""
	}
	best, longest := t.matchPath(words[start:])
	if stop, problem := t.partialPathProblem(words, start, best, longest); stop {
		return problem
	}
	if best == nil {
		if strings.HasSuffix(words[start], ":") {
			return ""
		}
		if longest == 0 && slices.Contains(reg.Tasks, words[start]) {
			if command := t.taskCommand(); command != nil {
				return t.commandFlags(words, start+1, command)
			}
			return ""
		}
		if longest == 0 && t.tasksKnown {
			return fmt.Sprintf("%s has no command or task %s", t.config.Name, words[start])
		}
		return ""
	}
	return t.commandFlags(words, i+len(best.Path), best)
}

func (t loadedTool) partialPathProblem(words []string, start int, best *toolCommand, longest int) (bool, string) {
	if longest > 0 && start+longest < len(words) {
		next := words[start+longest]
		if placeholder(next) || strings.HasSuffix(next, ":") || strings.Contains(next, "/") && (best == nil || len(best.Path) < longest || best.Rest == "none") {
			return true, ""
		}
		if !strings.HasPrefix(next, "-") && (best == nil || len(best.Path) < longest) && t.hasLongerPath(words[start:start+longest]) {
			return true, fmt.Sprintf("%s %s has no command %s", t.config.Name, strings.Join(words[start:start+longest], " "), next)
		}
	}
	return false, ""
}

func (t loadedTool) taskCommand() *toolCommand {
	if len(t.config.TaskCommand) == 0 {
		return nil
	}
	for i := range t.registry.Commands {
		if slices.Equal(t.registry.Commands[i].Path, t.config.TaskCommand) {
			return &t.registry.Commands[i]
		}
	}
	return nil
}

func (t loadedTool) matchPath(words []string) (*toolCommand, int) {
	var best *toolCommand
	longest := 0
	for j := range t.registry.Commands {
		cmd := &t.registry.Commands[j]
		matched := 0
		for matched < len(cmd.Path) && matched < len(words) && cmd.Path[matched] == words[matched] {
			matched++
		}
		if matched > longest {
			longest = matched
		}
		if matched == len(cmd.Path) && matched > 0 && (best == nil || matched > len(best.Path)) {
			best = cmd
		}
	}
	return best, longest
}

func (t loadedTool) hasLongerPath(prefix []string) bool {
	for _, cmd := range t.registry.Commands {
		if len(cmd.Path) > len(prefix) && slices.Equal(cmd.Path[:len(prefix)], prefix) {
			return true
		}
	}
	return false
}

func (t loadedTool) commandFlags(words []string, i int, best *toolCommand) string {
	reg := t.registry
	command := t.config.Name + " " + strings.Join(best.Path, " ")
	for i < len(words) {
		w := words[i]
		if placeholder(w) {
			return ""
		}
		if w == "--" {
			if best.Rest != "none" {
				return ""
			}
			i++
			continue
		}
		if strings.HasPrefix(w, "-") {
			flag := flagName(w)
			if !slices.Contains(best.Flags, flag) && !slices.Contains(reg.GlobalFlags, flag) {
				return fmt.Sprintf("%s has no flag %s", command, flag)
			}
			values := best.ValueFlags
			if slices.Contains(reg.GlobalValueFlags, flag) {
				values = reg.GlobalValueFlags
			}
			i = skipFlag(words, i, values)
			continue
		}
		if best.Rest == "none" {
			return fmt.Sprintf("%s takes no arguments: %s", command, w)
		}
		i++
	}
	return ""
}
