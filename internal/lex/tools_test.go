package lex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func splitToolCheck(c *toolChecker, path string, raw []byte) ([]string, []string) {
	return c.checkWithWarnings(path, raw)
}

func combinedToolCheck(c *toolChecker, path string, raw []byte) []string {
	warnings, problems := splitToolCheck(c, path, raw)
	return append(problems, warnings...)
}

func combinedDiskToolCheck(c *toolChecker, codex string) []string {
	warnings, problems := c.checkDiskFragmentsWithWarnings(codex)
	return append(problems, warnings...)
}

func requireToolMention(t *testing.T, c *toolChecker, path string, raw []byte, want string) {
	t.Helper()
	warnings, problems := splitToolCheck(c, path, raw)
	if len(problems) != 0 {
		t.Fatalf("mention check returned errors: %v", problems)
	}
	got := strings.Join(warnings, "\n")
	if want == "" && got != "" || want != "" && !strings.Contains(got, want) {
		t.Fatalf("warnings %q, want %q", got, want)
	}
}

func TestToolMentions(t *testing.T) {
	tool := loadedTool{loaded: true, config: configuredTool{Name: "graft", Help: []string{"graft", "help", "--json"}}, tasksKnown: true, registry: toolRegistry{
		GlobalFlags: []string{"-C", "--help"}, GlobalValueFlags: []string{"-C"}, Tasks: []string{"test"},
		Commands: []toolCommand{{Path: []string{"worktree"}, Flags: []string{"--short"}, Rest: "none"}, {Path: []string{"worktree", "add"}, Flags: []string{"--trunk"}, ValueFlags: []string{"--trunk"}}, {Path: []string{"commit"}, Flags: []string{"-m"}, ValueFlags: []string{"-m"}}},
	}}
	cases := []struct{ name, text, want string }{
		{"prose", "graft commit --bad", ""},
		{"command", "`graft -C '/some dir' worktree add --trunk=main`", ""},
		{"longest", "`graft worktree add --short`", "--short"},
		{"value", "`graft commit -m 'hello --bad'`", ""},
		{"placeholder value", "`graft commit -m <msg> --bad`", ""},
		{"unknown", "`graft unknown`", "unknown"},
		{"task", "`graft test`", ""},
		{"placeholder command", "`graft <task>`; `graft …`", ""},
		{"separator", "`echo ok && graft commit --bad`", "--bad"},
		{"quoted separator", "`echo 'x; graft commit --bad'`", ""},
		{"foreign command", "`graft commit -- go test --bad`", ""},
		{"fence", "\x60\x60\x60sh\n$ graft commit --bad\n\x60\x60\x60", "--bad"},
		{"unknown subcommand", "\x60graft worktree sync\x60", "takes no arguments: sync"},
		{"group", "\x60graft worktree\x60", ""},
		{"group placeholder", "\x60graft worktree <sub> --bad\x60", ""},
		{"group output", "\x60graft worktree sync: --bad\x60", ""},
		{"comment", "\x60graft commit # --bad\x60", ""},
		{"hash value", "\x60graft commit -m 'x#y' --bad\x60", "--bad"},
		{"sudo", "\x60sudo graft commit --bad\x60", "--bad"},
		{"env", "\x60env A=1 B=2 graft commit --bad\x60", "--bad"},
		{"assignment", "\x60A=1 B=2 graft commit --bad\x60", "--bad"},
		{"timeout", "\x60timeout 2s graft commit --bad\x60", "--bad"},
		{"subshell", "\x60(graft commit --bad)\x60", "--bad"},
		{"substitution", "\x60echo $(graft commit --bad)\x60", "--bad"},
		{"ampersand", "\x60echo x & graft commit --bad\x60", "--bad"},
		{"double pipe", "\x60echo x || graft commit --bad\x60", "--bad"},
		{"semicolon", "\x60echo x ; graft commit --bad\x60", "--bad"},
		{"pipe", "\x60echo x | graft commit --bad\x60", "--bad"},
		{"continuation", "\x60\x60\x60\ngraft commit \\\n --bad\n\x60\x60\x60", "--bad"},
		{"tilde", "~~~sh\ngraft commit --bad\n~~~", "--bad"},
		{"after fence", "~~~\necho ok\n~~~\ngraft commit --bad", ""},
		{"output", "\x60graft commit: --bad\x60", ""},
		{"middle prose", "\x60run graft commit --bad\x60", ""},
		{"global after path", "\x60graft commit -C /tmp --help\x60", ""},
		{"global value after path", "\x60graft commit -C --bad\x60", ""},
		{"global dashdash", "\x60graft -- foreign --bad\x60", ""},
		{"global missing value", "\x60graft -C\x60", ""},
		{"eq with next flag", "\x60graft worktree add --trunk=main --bad\x60", "--bad"},
		{"brace placeholder", "\x60graft commit {msg} --bad\x60", ""},
		{"dollar placeholder", "\x60graft commit $arg --bad\x60", ""},
		{"star placeholder", "\x60graft commit * --bad\x60", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := toolChecker{tools: []loadedTool{tool}, seen: map[string]bool{}}
			requireToolMention(t, &c, "frag/example.md", []byte("---\nid: example\nцель: test\nоснование: test\nснять_когда: test\n---\n"+tc.text), tc.want)
		})
	}
}

func TestToolRestAndCodeForms(t *testing.T) {
	tool := loadedTool{loaded: true, config: configuredTool{Name: "graft"}, tasksKnown: true, registry: toolRegistry{Commands: []toolCommand{
		{Path: []string{"deploy"}, Rest: "args", Flags: []string{"--ok"}},
		{Path: []string{"deploy", "freeze"}, Rest: "args"},
		{Path: []string{"worktree", "add"}, Rest: "args"},
		{Path: []string{"commit"}, Rest: "none", Flags: []string{"-m"}, ValueFlags: []string{"-m"}},
	}}}
	cases := []struct{ name, code, want string }{
		{"group argument", "\x60graft deploy prod --ok\x60", ""},
		{"group argument bad flag", "\x60graft deploy prod --bad\x60", "--bad"},
		{"slash argument bad flag", "\x60graft deploy path/prod --bad\x60", "--bad"},
		{"subcommand arguments", "\x60graft deploy freeze prod\x60", ""},
		{"group missing subcommand", "\x60graft worktree sync\x60", "has no command sync"},
		{"group alone", "\x60graft worktree\x60", ""},
		{"none argument", "\x60graft commit foo\x60", "takes no arguments: foo"},
		{"none dashdash flag", "\x60graft commit -- --bad\x60", "--bad"},
		{"escaped double quote", "\x60graft commit -m \"a \\\"b c\"\x60", ""},
		{"nohup", "\x60nohup graft commit --bad\x60", "--bad"},
		{"time", "\x60time graft commit --bad\x60", "--bad"},
		{"dots", "\x60graft ... --bad\x60", ""},
		{"brackets", "\x60graft [task] --bad\x60", ""},
		{"slash", "\x60graft start/stop --bad\x60", ""},
		{"sub slash", "\x60graft worktree add/remove --bad\x60", ""},
		{"comma", "\x60graft start, stop --bad\x60", ""},
		{"go code", "\x60\x60\x60go\ngraft commit --bad\n\x60\x60\x60", ""},
		{"shell code", "\x60\x60\x60bash\ngraft commit --bad\n\x60\x60\x60", "--bad"},
		{"heredoc", "\x60\x60\x60sh\ncat <<'EOF'\ngraft commit --bad\nEOF\ngraft commit --bad\n\x60\x60\x60", "--bad"},
		{"heredoc ignored", "~~~shell\ncat <<-EOF\n\tgraft commit --bad\n\tEOF\n~~~", ""},
		{"quoted heredoc text", "~~~sh\necho '<<EOF'\ngraft commit --bad\n~~~", "--bad"},
		{"comment heredoc text", "~~~sh\necho ok # <<EOF\ngraft commit --bad\n~~~", "--bad"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checker := toolChecker{tools: []loadedTool{tool}, seen: map[string]bool{}}
			requireToolMention(t, &checker, "frag.md", []byte("---\nid: x\nцель: x\nоснование: x\nснять_когда: x\n---\n"+tc.code), tc.want)
		})
	}
}

func TestToolReviewSixCases(t *testing.T) {
	tool := loadedTool{loaded: true, config: configuredTool{Name: "graft"}, tasksKnown: true, registry: toolRegistry{Commands: []toolCommand{
		{Path: []string{"worktree"}, Rest: "none"}, {Path: []string{"worktree", "add"}}, {Path: []string{"commit"}, Rest: "none", Flags: []string{"-m"}, ValueFlags: []string{"-m"}}, {Path: []string{"gate"}}, {Path: []string{"status"}}, {Path: []string{"deploy"}, Rest: "args"}, {Path: []string{"deploy", "freeze", "now"}},
	}}}
	for _, tc := range []struct{ name, text, want string }{
		{"slash none", "\x60graft worktree add/remove\x60", ""},
		{"heredoc quoted substitution", "\x60\x60\x60sh\ngraft commit -m \"$(cat <<'EOF'\ngraft commit --bad\nEOF\n)\"\n\x60\x60\x60", ""},
		{"heredoc backslash", "\x60\x60\x60sh\ncat <<\\EOF\ngraft commit --bad\nEOF\n\x60\x60\x60", ""},
		{"quoted substitution", "\x60v=\"$(graft commit --bad)\"\x60", "--bad"},
		{"quoted echo substitution", "\x60echo \"$(graft commit --bad)\"\x60", "--bad"},
		{"table pipe", "| \x60graft gate \\| graft status\x60 | x |", ""},
		{"bracket end", "\x60graft [x] ; graft commit --bad\x60", "--bad"},
		{"bracket in word", "\x60echo x[y ; graft commit --bad\x60", "--bad"},
		{"unquoted substitution before heredoc", "\x60\x60\x60sh\nv=$(graft status) && cat <<EOF\ngraft commit --bad\nEOF\n\x60\x60\x60", ""},
		{"quoted parenthesis in substitution before heredoc", "\x60\x60\x60sh\nv=\"$(echo ')' && cat <<EOF)\"\ngraft commit --bad\nEOF\n\x60\x60\x60", ""},
		{"subshell before heredoc", "\x60\x60\x60sh\n(cd x) && cat <<EOF\ngraft commit --bad\nEOF\n\x60\x60\x60", ""},
		{"closed substitution before heredoc", "\x60\x60\x60sh\ncat \"$(echo a)\" <<EOF\ngraft commit --bad\nEOF\n\x60\x60\x60", ""},
		{"substitution and subshell before heredoc", "\x60\x60\x60sh\necho \"$(echo a)\" && (cd x) && cat <<EOF\ngraft commit --bad\nEOF\n\x60\x60\x60", ""},
		{"escaped pipe outside table", "x \x60graft commit \\| graft status\x60", "takes no arguments"},
		{"unquoted substitution before command", "\x60v=$(graft status) && graft commit --bad\x60", "--bad"},
		{"quoted variable before command", "\x60echo \"$x\" && graft commit --bad\x60", "--bad"},
		{"quoted parenthesis in substitution before command", "\x60v=\"$(echo ')' && graft commit --bad)\"\x60", "--bad"},
		{"subshell before command", "\x60(cd x) && graft commit --bad\x60", "--bad"},
		{"closed substitution before command", "\x60v=\"$(graft status)\" && graft commit --bad\x60", "--bad"},
		{"text after substitution", "\x60echo \"$(graft commit) done\"\x60", ""},
		{"substitution and subshell before command", "\x60v=\"$(graft status)\" && (cd x) && graft commit --bad\x60", "--bad"},
		{"slash after partial nested path", "\x60graft deploy freeze a/b\x60", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := toolChecker{tools: []loadedTool{tool}, seen: map[string]bool{}}
			requireToolMention(t, &c, "frag.md", []byte("---\nid: x\nцель: x\nоснование: x\nснять_когда: x\n---\n"+tc.text), tc.want)
		})
	}
	for _, language := range []string{"shell", "zsh", "console", "sh title"} {
		sections := codeSections("\x60\x60\x60" + language + "\ngraft commit --bad\n\x60\x60\x60")
		if len(sections) != 1 || sections[0] != "graft commit --bad" {
			t.Fatalf("language %s: %v", language, sections)
		}
	}
	sections := codeSections("| `graft gate \\| graft status` | x |")
	if len(sections) != 1 {
		t.Fatalf("table sections: %v", sections)
	}
	commands := commandWords(sections[0])
	if len(commands) != 2 || len(commands[0]) != 2 || commands[0][1] != "gate" || len(commands[1]) != 2 || commands[1][1] != "status" {
		t.Fatalf("table commands: %v", commands)
	}
	if got := commandWords("echo \"x\\"); len(got) != 1 {
		t.Fatalf("trailing backslash: %v", got)
	}
	if got := codeSections("\x60\x60\x60sh\ncat <<A <<B\none\nA\ngraft commit --bad\nB\ngraft commit --bad\n\x60\x60\x60"); len(got) != 2 {
		t.Fatalf("two heredocs: %v", got)
	}
	if got := findHeredocs("cat < ignored <<A <<B"); len(got) != 2 || got[0].word != "A" || got[1].word != "B" {
		t.Fatalf("heredocs: %v", got)
	}
	if got := findHeredocs("cat <<'A <<B"); len(got) != 2 || got[1].word != "B" {
		t.Fatalf("incomplete heredoc example: %v", got)
	}
}

func TestToolReviewSixCacheAndSymlink(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "project")
	if err := os.Mkdir(project, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(project, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "registry")
	log := filepath.Join(dir, "calls")
	mustWrite(t, script, fmt.Appendf(nil, "#!/bin/sh\necho call >> %q\necho broken >&2\nexit 1\n", log))
	if err := os.Chmod(script, 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "tools.json"), fmt.Appendf(nil, `{"tools":[{"name":"graft","help":[%q],"dirs":[%q,%q]}]}`, script, project, filepath.Join(dir, "link")))
	c, err := loadToolChecker(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("---\nid: x\nцель: x\nоснование: x\nснять_когда: x\n---\n\x60graft commit\x60")
	warnings, problems := splitToolCheck(c, "one", raw)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "broken") || len(problems) != 0 {
		t.Fatalf("registry warning: warnings=%v problems=%v", warnings, problems)
	}
	secondWarnings, problems := splitToolCheck(c, "two", raw)
	if len(secondWarnings) != 0 || len(problems) != 0 {
		t.Fatalf("cached registry warning: warnings=%v problems=%v", secondWarnings, problems)
	}
	calls, err := os.ReadFile(log)
	if err != nil || strings.Count(string(calls), "call") != 1 {
		t.Fatalf("calls %q: %v", calls, err)
	}
	mustWrite(t, script, fmt.Appendf(nil, "#!/bin/sh\necho call >> %q\necho '{}'\n", log))
	c, err = loadToolChecker(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.tools[0].load(); err != nil {
		t.Fatal(err)
	}
	calls, err = os.ReadFile(log)
	if err != nil || strings.Count(string(calls), "call") != 2 {
		t.Fatalf("symlink calls %q: %v", calls, err)
	}
}

func TestToolReviewFiveCases(t *testing.T) {
	for _, tc := range []struct{ name, text, want string }{
		{"bracket words", "\x60graft commit [-m MSG]\x60", ""},
		{"bracket pipe", "\x60graft [start|graft commit --bad]\x60", ""},
		{"uppercase", "\x60\x60\x60Bash\ngraft commit --bad\n\x60\x60\x60", "--bad"},
		{"heredoc indent", "  \x60\x60\x60sh\n  cat <<EOF\n  hi\n  EOF\n  graft commit --bad\n  \x60\x60\x60", "--bad"},
		{"heredoc tabs", "\x60\x60\x60sh\ncat <<-EOF\n\thi\n\tEOF\ngraft commit --bad\n\x60\x60\x60", "--bad"},
		{"here string", "\x60\x60\x60sh\ncat <<<word\ngraft commit --bad\n\x60\x60\x60", "--bad"},
		{"sudo empty flag", "\x60sudo -E graft commit --bad\x60", "--bad"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := loadedTool{loaded: true, config: configuredTool{Name: "graft"}, tasksKnown: true, registry: toolRegistry{Commands: []toolCommand{{Path: []string{"commit"}, Rest: "none"}}}}
			c := toolChecker{tools: []loadedTool{tool}, seen: map[string]bool{}}
			requireToolMention(t, &c, "frag.md", []byte("---\nid: x\nцель: x\nоснование: x\nснять_когда: x\n---\n"+tc.text), tc.want)
		})
	}
	for _, flag := range []string{"-u", "-g", "-C", "-h", "-p", "-D", "-r", "-t", "-U"} {
		got := commandWords("sudo -E " + flag + " user graft commit --bad")
		if len(got) != 1 || len(got[0]) != 3 || got[0][0] != "graft" {
			t.Fatalf("sudo %s: %v", flag, got)
		}
	}
}

func TestToolReviewFiveMutants(t *testing.T) {
	dir := t.TempDir()
	if _, err := toolDirs(dir, "tools.json", configuredTool{Name: "x", Dirs: []string{"["}}); err == nil || !strings.Contains(err.Error(), "invalid directory glob") {
		t.Fatalf("invalid glob diagnostic: %v", err)
	}
	if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := toolDirs(dir, "tools.json", configuredTool{Name: "x", Dirs: []string{"link"}}); err == nil {
		t.Fatal("broken symlink accepted")
	}
	for _, tc := range []struct {
		line string
		want int
		tabs bool
	}{
		{`echo \<\<EOF`, 0, false},
		{`echo "hello" <<EOF`, 1, false},
		{`echo "a \" b" <<EOF`, 1, false},
		{`cat <<-EOF`, 1, true},
		{`cat <<<word`, 0, false},
	} {
		got := findHeredocs(tc.line)
		if len(got) != tc.want || len(got) > 0 && got[0].stripTabs != tc.tabs {
			t.Fatalf("heredocs %q: %v", tc.line, got)
		}
	}
	for _, tc := range []struct {
		text  string
		count int
	}{
		{"\x60\x60\x60sh\ncat <<EOF\n\x60\x60\x60\n\x60\x60\x60sh\ngraft commit --bad\n\x60\x60\x60", 2},
		{"\x60\x60\x60sh\necho x \\\n y\ngraft commit --bad\n\x60\x60\x60", 2},
	} {
		if got := codeSections(tc.text); len(got) != tc.count {
			t.Fatalf("sections: %v", got)
		}
	}
	for _, tc := range []struct {
		text string
		want string
	}{
		{`graft commit -m "a \"b c" --bad`, `a "b c`},
		{`graft commit -m "a \\ b" --bad`, `a \ b`},
	} {
		got := commandWords(tc.text)
		if len(got) != 1 || len(got[0]) != 5 || got[0][3] != tc.want || got[0][4] != "--bad" {
			t.Fatalf("words %q: %v", tc.text, got)
		}
	}
	if got := commandWords("echo ok # ignore; graft commit --bad"); len(got) != 1 {
		t.Fatalf("comment: %v", got)
	}
	if got := stripCommandPrefix([]string{"timeout"}); len(got) != 0 {
		t.Fatalf("timeout: %v", got)
	}
	tool := loadedTool{config: configuredTool{Name: "graft"}, tasksKnown: true, registry: toolRegistry{Commands: []toolCommand{{Path: []string{"group"}}, {Path: []string{"group", "sub", "add"}}, {Path: []string{"tree", "add"}}}}}
	if got := tool.problem([]string{"graft", "tree", "[x"}); got != "" {
		t.Fatalf("group placeholder: %s", got)
	}
	if got := tool.problem([]string{"graft", "group", "sub", "wrong"}); !strings.Contains(got, "has no command wrong") {
		t.Fatalf("shorter command: %s", got)
	}
}

func TestToolMoreReviewCases(t *testing.T) {
	tool := loadedTool{config: configuredTool{Name: "graft", TaskCommand: []string{"run"}}, tasksKnown: true, registry: toolRegistry{
		Tasks: []string{"worktree"}, GlobalFlags: []string{"-C"}, GlobalValueFlags: []string{"-C"},
		Commands: []toolCommand{{Path: []string{"worktree", "add"}}, {Path: []string{"commit"}}, {Path: []string{"run"}}},
	}}
	for _, tc := range []struct {
		words []string
		want  string
	}{
		{[]string{"graft", "-C", "plain", "commit", "--bad"}, "graft commit has no flag --bad"},
		{[]string{"graft", "--<"}, ""},
		{[]string{"graft", "worktree", "--bad"}, ""},
	} {
		got := tool.problem(tc.words)
		if got != tc.want {
			t.Fatalf("%v: got %q want %q", tc.words, got, tc.want)
		}
	}
	if !placeholder("<") {
		t.Fatal("opening angle placeholder")
	}
	empty := loadedTool{tasksKnown: true, config: configuredTool{Name: "graft"}, registry: toolRegistry{Commands: []toolCommand{{Path: []string{}}}}}
	if got := empty.problem([]string{"graft", "unknown"}); !strings.Contains(got, "unknown") {
		t.Fatalf("empty path: %q", got)
	}
	if got := empty.taskCommand(); got != nil {
		t.Fatalf("empty task command: %v", got)
	}
	cases := []struct {
		text string
		want int
	}{
		{"\x60\x60\x60sh\n~~~\ngraft commit --bad\n\x60\x60\x60", 2},
		{"\x60\x60\x60sh\ngraft commit --bad \\\n\x60\x60\x60", 1},
		{"\x60\x60\x60sh\ngraft commit --bad \\", 1},
	}
	for _, tc := range cases {
		if got := codeSections(tc.text); len(got) != tc.want {
			t.Fatalf("sections %q: %v", tc.text, got)
		}
	}
}

func TestToolReviewRegressions(t *testing.T) {
	tool := loadedTool{config: configuredTool{Name: "graft"}, tasksKnown: true, registry: toolRegistry{Commands: []toolCommand{
		{Path: []string{"worktree", "add"}}, {Path: []string{"other", "deep"}}, {Path: []string{"commit"}, Rest: "none", Flags: []string{"-m"}, ValueFlags: []string{"-m"}},
	}}}
	for _, tc := range []struct {
		words []string
		want  string
	}{
		{[]string{"graft", "--bad", "commit"}, "--bad"},
		{[]string{"graft", "worktree"}, ""},
		{[]string{"graft", "worktree", "--help"}, ""},
		{[]string{"graft", "worktree", "sync"}, "has no command sync"},
	} {
		got := tool.problem(tc.words)
		if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
			t.Fatalf("%v: %s", tc.words, got)
		}
	}
	if tool.hasLongerPath([]string{"other", "deep"}) || tool.hasLongerPath([]string{"missing"}) || !tool.hasLongerPath([]string{"worktree"}) {
		t.Fatal("incorrect prefix matching")
	}
	if got := commandWords(`graft commit -m "hello world" --bad`); len(got) != 1 || len(got[0]) != 5 || got[0][3] != "hello world" {
		t.Fatalf("quotes: %v", got)
	}
	if got := commandWords(`echo hash#value; graft commit --bad`); len(got) != 2 {
		t.Fatalf("hash: %v", got)
	}
	if got := commandWords(`(echo x)graft commit --bad`); len(got) != 2 {
		t.Fatalf("close parenthesis: %v", got)
	}
	for _, tc := range []struct {
		s    string
		want bool
	}{{"=x", false}, {"1a=x", false}, {"a-b=x", false}, {"a1=x", true}, {"_a=x", true}} {
		if assignment(tc.s) != tc.want {
			t.Fatalf("assignment %q", tc.s)
		}
	}
	if got := combinedDiskToolCheck(&toolChecker{tools: []loadedTool{{}}, seen: map[string]bool{}}, t.TempDir()); len(got) != 0 {
		t.Fatalf("missing frag directory: %v", got)
	}
	if got := (loadedTool{config: configuredTool{TaskCommand: []string{"jobs", "launch"}}, registry: toolRegistry{Commands: []toolCommand{{Path: []string{"jobs", "other"}}}}}).taskCommand(); got != nil {
		t.Fatalf("incorrect command match: %v", got)
	}
}

func TestToolInvalidConfigCheckModes(t *testing.T) {
	repo, codex, _ := stagedRepo(t)
	config := filepath.Join(codex, "tools.json")
	for _, data := range []string{"{", `{"tools":[{"name":"","help":["unused"]}]}`} {
		mustWrite(t, config, []byte(data))
		if _, err := CheckWithWarnings(codex, CheckOptions{}); err == nil {
			t.Fatalf("disk accepted %s", data)
		}
		if err := checkIn(t, repo, codex); err == nil {
			t.Fatalf("staged accepted %s", data)
		}
	}
}

func TestToolConfigurationAndTraversal(t *testing.T) {
	dir := t.TempDir()
	if c, err := loadToolChecker(dir); err != nil || len(c.tools) != 0 {
		t.Fatalf("missing config: %v", err)
	}
	config := filepath.Join(dir, "tools.json")
	if err := os.Mkdir(config, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := loadToolChecker(dir); err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("unreadable configuration: %v", err)
	}
	if err := os.Remove(config); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{`{"tools":[{"name":"graft"}]}`, `{"tools":[{"name":"graft","help":[""]}]}`} {
		mustWrite(t, config, []byte(data))
		if _, err := loadToolChecker(dir); err == nil {
			t.Fatalf("invalid configuration: %s", data)
		}
	}
	script := filepath.Join(dir, "registry")
	mustWrite(t, script, []byte("#!/bin/sh\npwd\n"))
	if err := os.Chmod(script, 0755); err != nil {
		t.Fatal(err)
	}
	if dirs, err := toolDirs(dir, config, configuredTool{Name: "x"}); err != nil || len(dirs) != 1 || dirs[0] != dir {
		t.Fatalf("default dir: %v %v", dirs, err)
	}
	if err := os.Mkdir(filepath.Join(dir, "project"), 0755); err != nil {
		t.Fatal(err)
	}
	if dirs, err := toolDirs(dir, config, configuredTool{Name: "x", Dirs: []string{"proj*"}}); err != nil || len(dirs) != 1 || dirs[0] != filepath.Join(dir, "project") {
		t.Fatalf("relative glob: %v %v", dirs, err)
	}
	if _, err := toolDirs(dir, config, configuredTool{Name: "x", Dirs: []string{"missing*"}}); err == nil {
		t.Fatal("missing directories")
	}
	checker := &toolChecker{tools: []loadedTool{{}}, seen: map[string]bool{}}
	if got := combinedToolCheck(checker, "bad.md", []byte("bad")); len(got) != 1 {
		t.Fatalf("bad fragment: %v", got)
	}
	if got := combinedToolCheck(checker, "bad.md", []byte("bad")); len(got) != 0 {
		t.Fatalf("duplicate fragment: %v", got)
	}
	root := filepath.Join(dir, "frag")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "other.txt"), []byte("bad"))
	if got := combinedDiskToolCheck(checker, dir); len(got) != 0 {
		t.Fatalf("non-md: %v", got)
	}
	mustWrite(t, filepath.Join(root, "bad.md"), []byte("bad"))
	if got := combinedDiskToolCheck(&toolChecker{seen: map[string]bool{}}, dir); len(got) != 0 {
		t.Fatalf("disabled: %v", got)
	}
	if got := combinedDiskToolCheck(checker, config); len(got) == 0 {
		t.Fatal("walk error ignored")
	}
}

func TestToolRegistryLazyAndCached(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "registry")
	log := filepath.Join(dir, "calls")
	mustWrite(t, script, fmt.Appendf(nil, "#!/bin/sh\necho call >> %q\necho '{\"commands\":[{\"path\":[\"commit\"]}]}'\n", log))
	if err := os.Chmod(script, 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "tools.json"), fmt.Appendf(nil, `{"tools":[{"name":"graft","help":[%q]}]}`, script))
	c, err := loadToolChecker(dir)
	if err != nil {
		t.Fatal(err)
	}
	header := "---\nid: x\nцель: x\nоснование: x\nснять_когда: x\n---\n"
	if got := combinedToolCheck(c, "one", []byte(header+"\x60go build\x60")); len(got) != 0 {
		t.Fatal(got)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("help ran without mention: %v", err)
	}
	for _, name := range []string{"two", "three"} {
		if got := combinedToolCheck(c, name, []byte(header+"\x60graft commit\x60")); len(got) != 0 {
			t.Fatal(got)
		}
	}
	calls, err := os.ReadFile(log)
	if err != nil || strings.Count(string(calls), "call") != 1 {
		t.Fatalf("calls %q: %v", calls, err)
	}
	if err := os.Remove(script); err != nil {
		t.Fatal(err)
	}
	c, err = loadToolChecker(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := combinedToolCheck(c, "four", []byte(header+"no mentions")); len(got) != 0 {
		t.Fatal(got)
	}
	warnings, problems := splitToolCheck(c, "five", []byte(header+"\x60graft commit\x60"))
	if len(warnings) == 0 || len(problems) != 0 {
		t.Fatal("broken help accepted")
	}
}

func TestToolUnavailableTasks(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "registry")
	mustWrite(t, script, []byte("#!/bin/sh\necho '{\"tasks_error\":\"outside\"}'\n"))
	if err := os.Chmod(script, 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "tools.json"), fmt.Appendf(nil, `{"tools":[{"name":"graft","help":[%q]}]}`, script))
	c, err := loadToolChecker(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.tools[0].load(); err != nil {
		t.Fatal(err)
	}
	if got := c.tools[0].problem([]string{"graft", "unknown"}); got != "" {
		t.Fatalf("tasks unavailable: %s", got)
	}
	mustWrite(t, script, []byte("#!/bin/sh\necho '{\"tasks\":[\"test\"]}'\n"))
	c, err = loadToolChecker(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.tools[0].load(); err != nil {
		t.Fatal(err)
	}
	if got := c.tools[0].problem([]string{"graft", "unknown"}); got == "" {
		t.Fatal("unknown task accepted")
	}
}

func TestToolTaskCommand(t *testing.T) {
	tool := loadedTool{
		config: configuredTool{Name: "tool", TaskCommand: []string{"jobs", "launch"}}, tasksKnown: true,
		registry: toolRegistry{
			Tasks: []string{"test"}, GlobalFlags: []string{"-C"}, GlobalValueFlags: []string{"-C"},
			Commands: []toolCommand{{Path: []string{"jobs", "launch"}, Flags: []string{"--no-cache", "--since"}, ValueFlags: []string{"--since"}}},
		},
	}
	cases := []struct {
		name  string
		words []string
		want  string
	}{
		{"known flag", []string{"tool", "test", "--no-cache"}, ""},
		{"unknown flag", []string{"tool", "test", "--bad"}, "--bad"},
		{"dashdash", []string{"tool", "test", "--no-cache", "--", "--bad"}, ""},
		{"global flag", []string{"tool", "test", "-C", "--bad"}, ""},
		{"value flag", []string{"tool", "test", "--since", "--bad"}, ""},
		{"equals flag", []string{"tool", "test", "--since=x", "--bad"}, "--bad"},
		{"placeholder", []string{"tool", "test", "<args>", "--bad"}, ""},
		{"unknown task", []string{"tool", "missing", "--no-cache"}, "missing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tool.problem(tc.words)
			if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	tool.config.TaskCommand = nil
	if got := tool.problem([]string{"tool", "test", "--bad"}); got != "" {
		t.Fatalf("without task_command: %s", got)
	}
}

func TestToolMissingTaskCommand(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "registry")
	mustWrite(t, script, []byte("#!/bin/sh\necho '{\"commands\":[{\"path\":[\"other\"]}],\"tasks\":[\"test\"]}'\n"))
	if err := os.Chmod(script, 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "tools.json"), fmt.Appendf(nil, `{"tools":[{"name":"tool","help":[%q],"task_command":["jobs","launch"]}]}`, script))
	checker, err := loadToolChecker(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("---\nid: x\nцель: x\nоснование: x\nснять_когда: x\n---\n\x60tool test\x60")
	warnings, problems := splitToolCheck(checker, "frag.md", raw)
	got := strings.Join(warnings, "\n")
	if len(problems) != 0 || !strings.Contains(got, "task_command") || !strings.Contains(got, "jobs launch") || !strings.Contains(got, "tool") {
		t.Fatalf("missing command warning: warnings=%s problems=%v", got, problems)
	}
}

func TestToolPathFlagName(t *testing.T) {
	tool := loadedTool{config: configuredTool{Name: "tool"}, registry: toolRegistry{Commands: []toolCommand{{Path: []string{"group", "-sub"}}}}}
	if got := tool.problem([]string{"tool", "group", "-sub"}); got != "" {
		t.Fatal(got)
	}
}

func TestToolCheckDiskAndStaged(t *testing.T) {
	repo, codex, git := stagedRepo(t)
	script := filepath.Join(repo, "registry")
	log := filepath.Join(repo, "calls")
	mustWrite(t, script, fmt.Appendf(nil, "#!/bin/sh\necho called >> %q\necho '{\"commands\":[{\"path\":[\"commit\"]}]}'\n", log))
	if err := os.Chmod(script, 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(codex, "tools.json"), fmt.Appendf(nil, `{"tools":[{"name":"graft","help":[%q]}]}`, script))
	frag := fragmentOf(t, codex, "a.md")
	original, err := os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	bad := append(append([]byte{}, original...), []byte("\n\x60graft commit --bad\x60\n")...)
	mustWrite(t, frag, bad)
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil || !strings.Contains(strings.Join(warnings, "\n"), "--bad") || !strings.Contains(strings.Join(warnings, "\n"), frag) {
		t.Fatalf("disk warnings=%v err=%v", warnings, err)
	}
	warnings, err = CheckWithWarnings(codex, CheckOptions{OutputsUnder: repo})
	if err != nil || !strings.Contains(strings.Join(warnings, "\n"), "--bad") || !strings.Contains(strings.Join(warnings, "\n"), frag) {
		t.Fatalf("outputs-under warnings=%v err=%v", warnings, err)
	}
	if err := git("add", frag); err != nil {
		t.Fatal(err)
	}
	if err := git("add", "a.md"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, frag, original)
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	warnings, stagedErr := CheckWithWarnings(codex, CheckOptions{Staged: true})
	if err := os.Chdir(old); err != nil {
		t.Fatal(err)
	}
	if stagedErr != nil || !strings.Contains(strings.Join(warnings, "\n"), "--bad") || !strings.Contains(strings.Join(warnings, "\n"), frag) {
		t.Fatalf("index warnings=%v err=%v", warnings, stagedErr)
	}
	mustWrite(t, filepath.Join(repo, "unrelated"), []byte("x"))
	if err := git("add", "unrelated"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(script); err != nil {
		t.Fatal(err)
	}
	if err := checkIn(t, repo, codex); err != nil {
		t.Fatalf("unrelated: %v", err)
	}
}

func TestToolBrokenRegistryWarnsOnce(t *testing.T) {
	_, codex, _ := stagedRepo(t)
	script := filepath.Join(t.TempDir(), "registry")
	mustWrite(t, script, []byte("#!/bin/sh\necho registry-unavailable >&2\nexit 1\n"))
	if err := os.Chmod(script, 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(codex, "tools.json"), fmt.Appendf(nil, `{"tools":[{"name":"graft","help":[%q]}]}`, script))
	frag := fragmentOf(t, codex, "a.md")
	raw, err := os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, frag, append(append([]byte{}, raw...), []byte("\n`graft commit`\n`graft worktree`\n")...))
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	joined := strings.Join(warnings, "\n")
	if err != nil || len(warnings) != 1 || strings.Count(joined, "registry-unavailable") != 1 || !strings.Contains(joined, "graft") {
		t.Fatalf("warnings=%v err=%v", warnings, err)
	}
}

func TestToolRegistryWarningOmitsEmptyStderr(t *testing.T) {
	for name, body := range map[string]string{
		"not json": "#!/bin/sh\necho not-json\n",
		"failed":   "#!/bin/sh\nexit 3\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, codex, _ := stagedRepo(t)
			script := filepath.Join(t.TempDir(), "registry")
			mustWrite(t, script, []byte(body))
			if err := os.Chmod(script, 0755); err != nil {
				t.Fatal(err)
			}
			mustWrite(t, filepath.Join(codex, "tools.json"), fmt.Appendf(nil, `{"tools":[{"name":"graft","help":[%q]}]}`, script))
			frag := fragmentOf(t, codex, "a.md")
			raw, err := os.ReadFile(frag)
			if err != nil {
				t.Fatal(err)
			}
			mustWrite(t, frag, append(append([]byte{}, raw...), []byte("\n`graft commit`\n")...))
			if err := Build(codex, false); err != nil {
				t.Fatal(err)
			}
			warnings, err := CheckWithWarnings(codex, CheckOptions{})
			if err != nil || len(warnings) != 1 || strings.HasSuffix(strings.TrimSpace(warnings[0]), ":") {
				t.Fatalf("warnings=%q err=%v; want one warning that does not end with a colon", warnings, err)
			}
		})
	}
}

func TestToolRegistryWarningKeepsStderrOfInvalidJSON(t *testing.T) {
	_, codex, _ := stagedRepo(t)
	script := filepath.Join(t.TempDir(), "registry")
	mustWrite(t, script, []byte("#!/bin/sh\necho not-json\necho registry-note >&2\n"))
	if err := os.Chmod(script, 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(codex, "tools.json"), fmt.Appendf(nil, `{"tools":[{"name":"graft","help":[%q]}]}`, script))
	frag := fragmentOf(t, codex, "a.md")
	raw, err := os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, frag, append(append([]byte{}, raw...), []byte("\n`graft commit`\n")...))
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil || len(warnings) != 1 || !strings.Contains(warnings[0], "invalid JSON") || !strings.HasSuffix(warnings[0], ": registry-note") {
		t.Fatalf("warnings=%q err=%v; want one invalid JSON warning ending with the stderr text", warnings, err)
	}
}

func TestToolLongestPathBothOrders(t *testing.T) {
	short := toolCommand{Path: []string{"worktree"}, Flags: []string{"--short"}, Rest: "none"}
	long := toolCommand{Path: []string{"worktree", "add"}, Flags: []string{"--long"}}
	for _, commands := range [][]toolCommand{{short, long}, {long, short}} {
		tool := loadedTool{config: configuredTool{Name: "graft"}, registry: toolRegistry{Commands: commands}, tasksKnown: true}
		if got := tool.problem([]string{"graft", "worktree", "add", "--short"}); !strings.Contains(got, "--short") {
			t.Fatalf("commands %v: %q", commands, got)
		}
		if got := tool.problem([]string{"graft", "worktree", "add", "--long"}); got != "" {
			t.Fatal(got)
		}
	}
}

func TestToolRegistryDirectoriesAndErrors(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"one", "two"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(dir, "registry")
	log := filepath.Join(dir, "calls")
	data := fmt.Sprintf("#!/bin/sh\necho x >> %q\ncase $PWD in */one) echo '{\"tasks\":[\"one\"]}' ;; *) echo '{\"tasks\":[\"two\"]}' ;; esac\n", log)
	if err := os.WriteFile(script, []byte(data), 0755); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`{"tools":[{"name":"graft","help":[%q],"dirs":[%q,%q]}]}`, script, filepath.Join(dir, "*"), filepath.Join(dir, "one"))
	if err := os.WriteFile(filepath.Join(dir, "tools.json"), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	checker, err := loadToolChecker(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := checker.tools[0].load(); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(calls), "x") != 2 {
		t.Fatalf("calls: %q", calls)
	}
	for _, task := range []string{"one", "two"} {
		if got := checker.tools[0].problem([]string{"graft", task}); got != "" {
			t.Fatal(got)
		}
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho broken >&2\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	checker, err = loadToolChecker(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := checker.tools[0].load(); err == nil || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("expected stderr, got %v", err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho '{}'\necho broken >&2\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	checker, err = loadToolChecker(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := checker.tools[0].load(); err == nil || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("expected failed command despite JSON output: %v", err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho broken >&2\necho invalid\n"), 0755); err != nil {
		t.Fatal(err)
	}
	checker, err = loadToolChecker(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := checker.tools[0].load(); err == nil || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("expected JSON error and stderr, got %v", err)
	}
}
